package core

import (
	"github.com/remeh/sizedwaitgroup"
	"go.uber.org/atomic"

	"github.com/projectdiscovery/gologger"
	"github.com/projectdiscovery/nuclei/v2/pkg/core/inputs"
	"github.com/projectdiscovery/nuclei/v2/pkg/output"
	"github.com/projectdiscovery/nuclei/v2/pkg/protocols/common/contextargs"
	"github.com/projectdiscovery/nuclei/v2/pkg/templates"
	"github.com/projectdiscovery/nuclei/v2/pkg/templates/types"
	generalTypes "github.com/projectdiscovery/nuclei/v2/pkg/types"
	stringsutil "github.com/projectdiscovery/utils/strings"
)

// Execute takes a list of templates/workflows that have been compiled
// and executes them based on provided concurrency options.
//
// All the execution logic for the templates/workflows happens in this part
// of the engine.
func (e *Engine) Execute(templates []*templates.Template, target InputProvider) *atomic.Bool {
	return e.ExecuteScanWithOpts(templates, target, false)
}

// executeTemplateSpray executes scan using template spray strategy where targets are iterated over each template
func (e *Engine) executeTemplateSpray(templatesList []*templates.Template, target InputProvider) *atomic.Bool {
	results := &atomic.Bool{}
	for _, template := range templatesList {
		templateType := template.Type()

		var wg *sizedwaitgroup.SizedWaitGroup
		if templateType == types.HeadlessProtocol {
			wg = e.workPool.Headless
		} else {
			wg = e.workPool.Default
		}

		wg.Add()
		go func(tpl *templates.Template) {
			defer wg.Done()

			switch {
			case tpl.SelfContained:
				// Self Contained requests are executed here separately
				e.executeSelfContainedTemplateWithInput(tpl, results)
			default:
				// All other request types are executed here
				e.executeModelWithInput(templateType, tpl, target, results)
			}
		}(template)
	}
	e.workPool.Wait()
	return results
}

// executeHostSpray executes scan using host spray strategy where templates are iterated over each target
func (e *Engine) executeHostSpray(templatesList []*templates.Template, target InputProvider) *atomic.Bool {
	results := &atomic.Bool{}
	hostwg := sizedwaitgroup.New(e.options.BulkSize)
	target.Scan(func(value *contextargs.MetaInput) bool {
		host := inputs.SimpleInputProvider{
			Inputs: []*contextargs.MetaInput{
				value,
			},
		}
		hostwg.Add()
		go func(result *atomic.Bool) {
			defer hostwg.Done()
			status := e.executeTemplateSpray(templatesList, &host)
			results.CompareAndSwap(false, status.Load())
		}(results)
		return true
	})
	hostwg.Wait()
	return results
}

// ExecuteScanWithOpts executes scan with given scanStatergy
func (e *Engine) ExecuteScanWithOpts(templatesList []*templates.Template, target InputProvider, noCluster bool) *atomic.Bool {
	var results *atomic.Bool

	var finalTemplates []*templates.Template
	if !noCluster {
		finalTemplates, _ = templates.ClusterTemplates(templatesList, e.executerOpts)
	} else {
		finalTemplates = templatesList
	}

	if stringsutil.EqualFoldAny(e.options.ScanStrategy, "auto", "") {
		// TODO: this is only a placeholder, auto scan strategy should choose scan strategy
		// based on no of hosts , templates , stream and other optimization parameters
		e.options.ScanStrategy = "template-spray"
	}
	switch e.options.ScanStrategy {
	case "template-spray":
		results = e.executeTemplateSpray(finalTemplates, target)
	case "host-spray":
		results = e.executeHostSpray(finalTemplates, target)
	}
	return results
}

// processSelfContainedTemplates execute a self-contained template.
func (e *Engine) executeSelfContainedTemplateWithInput(template *templates.Template, results *atomic.Bool) {
	match, err := template.Executer.Execute(contextargs.New())
	if err != nil {
		gologger.Warning().Msgf("[%s] Could not execute step: %s\n", e.executerOpts.Colorizer.BrightBlue(template.ID), err)
	}
	results.CompareAndSwap(false, match)
}

// executeModelWithInput executes a type of template with input
func (e *Engine) executeModelWithInput(templateType types.ProtocolType, template *templates.Template, target InputProvider, results *atomic.Bool) {
	wg := e.workPool.InputPool(templateType)

	var (
		index uint32
	)

	e.executerOpts.ResumeCfg.Lock()
	currentInfo, ok := e.executerOpts.ResumeCfg.Current[template.ID]
	if !ok {
		currentInfo = &generalTypes.ResumeInfo{}
		e.executerOpts.ResumeCfg.Current[template.ID] = currentInfo
	}
	if currentInfo.InFlight == nil {
		currentInfo.InFlight = make(map[uint32]struct{})
	}
	resumeFromInfo, ok := e.executerOpts.ResumeCfg.ResumeFrom[template.ID]
	if !ok {
		resumeFromInfo = &generalTypes.ResumeInfo{}
		e.executerOpts.ResumeCfg.ResumeFrom[template.ID] = resumeFromInfo
	}
	e.executerOpts.ResumeCfg.Unlock()

	// track progression
	cleanupInFlight := func(index uint32) {
		currentInfo.Lock()
		delete(currentInfo.InFlight, index)
		currentInfo.Unlock()
	}

	target.Scan(func(scannedValue *contextargs.MetaInput) bool {
		// Best effort to track the host progression
		// skips indexes lower than the minimum in-flight at interruption time
		var skip bool
		if resumeFromInfo.Completed { // the template was completed
			gologger.Debug().Msgf("[%s] Skipping \"%s\": Resume - Template already completed\n", template.ID, scannedValue)
			skip = true
		} else if index < resumeFromInfo.SkipUnder { // index lower than the sliding window (bulk-size)
			gologger.Debug().Msgf("[%s] Skipping \"%s\": Resume - Target already processed\n", template.ID, scannedValue)
			skip = true
		} else if _, isInFlight := resumeFromInfo.InFlight[index]; isInFlight { // the target wasn't completed successfully
			gologger.Debug().Msgf("[%s] Repeating \"%s\": Resume - Target wasn't completed\n", template.ID, scannedValue)
			// skip is already false, but leaving it here for clarity
			skip = false
		} else if index > resumeFromInfo.DoAbove { // index above the sliding window (bulk-size)
			// skip is already false - but leaving it here for clarity
			skip = false
		}

		currentInfo.Lock()
		currentInfo.InFlight[index] = struct{}{}
		currentInfo.Unlock()

		// Skip if the host has had errors
		if e.executerOpts.HostErrorsCache != nil && e.executerOpts.HostErrorsCache.Check(scannedValue.ID()) {
			return true
		}

		wg.WaitGroup.Add()
		go func(index uint32, skip bool, value *contextargs.MetaInput) {
			defer wg.WaitGroup.Done()
			defer cleanupInFlight(index)
			if skip {
				return
			}

			var match bool
			var err error
			switch templateType {
			case types.WorkflowProtocol:
				match = e.executeWorkflow(value, template.CompiledWorkflow)
			default:
				ctxArgs := contextargs.New()
				ctxArgs.MetaInput = value
				match, err = template.Executer.Execute(ctxArgs)
			}
			if err != nil {
				gologger.Warning().Msgf("[%s] Could not execute step: %s\n", e.executerOpts.Colorizer.BrightBlue(template.ID), err)
			}
			results.CompareAndSwap(false, match)
		}(index, skip, scannedValue)

		index++
		return true
	})
	wg.WaitGroup.Wait()

	// on completion marks the template as completed
	currentInfo.Lock()
	currentInfo.Completed = true
	currentInfo.Unlock()
}

// ExecuteWithResults a list of templates with results
func (e *Engine) ExecuteWithResults(templatesList []*templates.Template, target InputProvider, callback func(*output.ResultEvent)) *atomic.Bool {
	results := &atomic.Bool{}
	for _, template := range templatesList {
		templateType := template.Type()

		var wg *sizedwaitgroup.SizedWaitGroup
		if templateType == types.HeadlessProtocol {
			wg = e.workPool.Headless
		} else {
			wg = e.workPool.Default
		}

		wg.Add()
		go func(tpl *templates.Template) {
			e.executeModelWithInputAndResult(templateType, tpl, target, results, callback)
			wg.Done()
		}(template)
	}
	e.workPool.Wait()
	return results
}

// executeModelWithInputAndResult executes a type of template with input and result
func (e *Engine) executeModelWithInputAndResult(templateType types.ProtocolType, template *templates.Template, target InputProvider, results *atomic.Bool, callback func(*output.ResultEvent)) {
	wg := e.workPool.InputPool(templateType)

	target.Scan(func(scannedValue *contextargs.MetaInput) bool {
		// Skip if the host has had errors
		if e.executerOpts.HostErrorsCache != nil && e.executerOpts.HostErrorsCache.Check(scannedValue.ID()) {
			return true
		}

		wg.WaitGroup.Add()
		go func(value *contextargs.MetaInput) {
			defer wg.WaitGroup.Done()

			var match bool
			var err error
			switch templateType {
			case types.WorkflowProtocol:
				match = e.executeWorkflow(value, template.CompiledWorkflow)
			default:
				ctxArgs := contextargs.New()
				ctxArgs.MetaInput = value
				err = template.Executer.ExecuteWithResults(ctxArgs, func(event *output.InternalWrappedEvent) {
					for _, result := range event.Results {
						callback(result)
					}
				})
			}
			if err != nil {
				gologger.Warning().Msgf("[%s] Could not execute step: %s\n", e.executerOpts.Colorizer.BrightBlue(template.ID), err)
			}
			results.CompareAndSwap(false, match)
		}(scannedValue)
		return true
	})
	wg.WaitGroup.Wait()
}

type ChildExecuter struct {
	e *Engine

	results *atomic.Bool
}

// Close closes the executer returning bool results
func (e *ChildExecuter) Close() *atomic.Bool {
	e.e.workPool.Wait()
	return e.results
}

// Execute executes a template and URLs
func (e *ChildExecuter) Execute(template *templates.Template, value *contextargs.MetaInput) {
	templateType := template.Type()

	var wg *sizedwaitgroup.SizedWaitGroup
	if templateType == types.HeadlessProtocol {
		wg = e.e.workPool.Headless
	} else {
		wg = e.e.workPool.Default
	}

	wg.Add()
	go func(tpl *templates.Template) {
		defer wg.Done()

		ctxArgs := contextargs.New()
		ctxArgs.MetaInput = value
		match, err := template.Executer.Execute(ctxArgs)
		if err != nil {
			gologger.Warning().Msgf("[%s] Could not execute step: %s\n", e.e.executerOpts.Colorizer.BrightBlue(template.ID), err)
		}
		e.results.CompareAndSwap(false, match)
	}(template)
}

// ExecuteWithOpts executes with the full options
func (e *Engine) ChildExecuter() *ChildExecuter {
	return &ChildExecuter{
		e:       e,
		results: &atomic.Bool{},
	}
}
