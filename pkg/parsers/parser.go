package parsers

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"

	"github.com/projectdiscovery/nuclei/v3/pkg/catalog"
	"github.com/projectdiscovery/nuclei/v3/pkg/catalog/config"
	"github.com/projectdiscovery/nuclei/v3/pkg/catalog/loader/filter"
	"github.com/projectdiscovery/nuclei/v3/pkg/templates"
	"github.com/projectdiscovery/nuclei/v3/pkg/templates/cache"
	"github.com/projectdiscovery/nuclei/v3/pkg/templates/types"
	"github.com/projectdiscovery/nuclei/v3/pkg/utils"
	"github.com/projectdiscovery/nuclei/v3/pkg/utils/stats"
	errorutil "github.com/projectdiscovery/utils/errors"
	"gopkg.in/yaml.v2"
)

var (
	ErrMandatoryFieldMissingFmt = errorutil.NewWithFmt("mandatory '%s' field is missing")
	ErrInvalidField             = errorutil.NewWithFmt("invalid field format for '%s' (allowed format is %s)")
	ErrWarningFieldMissing      = errorutil.NewWithFmt("field '%s' is missing")
	ErrCouldNotLoadTemplate     = errorutil.NewWithFmt("Could not load template %s: %s")
	ErrLoadedWithWarnings       = errorutil.NewWithFmt("Loaded template %s: with syntax warning : %s")
)

// LoadTemplate returns true if the template is valid and matches the filtering criteria.
func LoadTemplate(templatePath string, tagFilter *filter.TagFilter, extraTags []string, catalog catalog.Catalog) (bool, error) {
	template, templateParseError := ParseTemplate(templatePath, catalog)
	if templateParseError != nil {
		return false, fmt.Errorf(CouldNotLoadTemplate, templatePath, templateParseError)
	}

	if len(template.Workflows) > 0 {
		return false, nil
	}

	validationError := validateTemplateMandatoryFields(template)
	if validationError != nil {
		stats.Increment(SyntaxErrorStats)
		return false, ErrCouldNotLoadTemplate.Msgf(templatePath, validationError)
	}

	ret, err := isTemplateInfoMetadataMatch(tagFilter, template, extraTags)
	if err != nil {
		return ret, ErrCouldNotLoadTemplate.Msgf(templatePath, err)
	}
	// if template loaded then check the template for optional fields to add warnings
	if ret {
		validationWarning := validateTemplateOptionalFields(template)
		if validationWarning != nil {
			stats.Increment(SyntaxWarningStats)
			return ret, ErrCouldNotLoadTemplate.Msgf(templatePath, validationWarning)
		}
	}
	return ret, nil
}

// LoadWorkflow returns true if the workflow is valid and matches the filtering criteria.
func LoadWorkflow(templatePath string, catalog catalog.Catalog) (bool, error) {
	template, templateParseError := ParseTemplate(templatePath, catalog)
	if templateParseError != nil {
		return false, templateParseError
	}

	if len(template.Workflows) > 0 {
		if validationError := validateTemplateMandatoryFields(template); validationError != nil {
			stats.Increment(SyntaxErrorStats)
			return false, validationError
		}
		return true, nil
	}

	return false, nil
}

func isTemplateInfoMetadataMatch(tagFilter *filter.TagFilter, template *templates.Template, extraTags []string) (bool, error) {
	match, err := tagFilter.Match(template, extraTags)

	if err == filter.ErrExcluded {
		return false, filter.ErrExcluded
	}

	return match, err
}

// validateTemplateMandatoryFields validates the mandatory fields of a template
// return error from this function will cause hard fail and not proceed further
func validateTemplateMandatoryFields(template *templates.Template) error {
	info := template.Info

	var validateErrors []error

	if utils.IsBlank(info.Name) {
		validateErrors = append(validateErrors, ErrMandatoryFieldMissingFmt.Msgf("name"))
	}

	if info.Authors.IsEmpty() {
		validateErrors = append(validateErrors, ErrMandatoryFieldMissingFmt.Msgf("author"))
	}

	if template.ID == "" {
		validateErrors = append(validateErrors, ErrMandatoryFieldMissingFmt.Msgf("id"))
	} else if !templateIDRegexp.MatchString(template.ID) {
		validateErrors = append(validateErrors, ErrInvalidField.Msgf("id", templateIDRegexp.String()))
	}

	if len(validateErrors) > 0 {
		return errors.Join(validateErrors...)
	}

	return nil
}

// validateTemplateOptionalFields validates the optional fields of a template
// return error from this function will throw a warning and proceed further
func validateTemplateOptionalFields(template *templates.Template) error {
	info := template.Info

	var warnings []error

	if template.Type() != types.WorkflowProtocol && utils.IsBlank(info.SeverityHolder.Severity.String()) {
		warnings = append(warnings, ErrWarningFieldMissing.Msgf("severity"))
	}

	if len(warnings) > 0 {
		return errors.Join(warnings...)
	}

	return nil
}

var (
	parsedTemplatesCache *cache.Templates
	ShouldValidate       bool
	NoStrictSyntax       bool
	templateIDRegexp     = regexp.MustCompile(`^([a-zA-Z0-9]+[-_])*[a-zA-Z0-9]+$`)
)

const (
	SyntaxWarningStats       = "syntax-warnings"
	SyntaxErrorStats         = "syntax-errors"
	RuntimeWarningsStats     = "runtime-warnings"
	UnsignedCodeWarning      = "unsigned-warnings"
	HeadlessFlagWarningStats = "headless-flag-missing-warnings"
	TemplatesExecutedStats   = "templates-executed"
	CodeFlagWarningStats     = "code-flag-missing-warnings"
	// Note: this is redefined in workflows.go to avoid circular dependency, so make sure to keep it in sync
	SkippedUnsignedStats = "skipped-unsigned-stats" // tracks loading of unsigned templates
)

func init() {
	parsedTemplatesCache = cache.New()
	config.DefaultConfig.RegisterGlobalCache(parsedTemplatesCache)

	stats.NewEntry(SyntaxWarningStats, "Found %d templates with syntax warning (use -validate flag for further examination)")
	stats.NewEntry(SyntaxErrorStats, "Found %d templates with syntax error (use -validate flag for further examination)")
	stats.NewEntry(RuntimeWarningsStats, "Found %d templates with runtime error (use -validate flag for further examination)")
	stats.NewEntry(UnsignedCodeWarning, "Found %d unsigned or tampered code template (carefully examine before using it & use -sign flag to sign them)")
	stats.NewEntry(HeadlessFlagWarningStats, "Excluded %d headless template[s] (disabled as default), use -headless option to run headless templates.")
	stats.NewEntry(CodeFlagWarningStats, "Excluded %d code template[s] (disabled as default), use -code option to run code templates.")
	stats.NewEntry(TemplatesExecutedStats, "Excluded %d template[s] with known weak matchers / tags excluded from default run using .nuclei-ignore")
	stats.NewEntry(SkippedUnsignedStats, "Skipping %d unsigned template[s]")
}

// ParseTemplate parses a template and returns a *templates.Template structure
func ParseTemplate(templatePath string, catalog catalog.Catalog) (*templates.Template, error) {
	if value, err := parsedTemplatesCache.Has(templatePath); value != nil {
		return value.(*templates.Template), err
	}
	data, err := utils.ReadFromPathOrURL(templatePath, catalog)
	if err != nil {
		return nil, err
	}

	template := &templates.Template{}

	switch config.GetTemplateFormatFromExt(templatePath) {
	case config.JSON:
		err = json.Unmarshal(data, template)
	case config.YAML:
		if NoStrictSyntax {
			err = yaml.Unmarshal(data, template)
		} else {
			err = yaml.UnmarshalStrict(data, template)
		}
	default:
		err = fmt.Errorf("failed to identify template format expected JSON or YAML but got %v", templatePath)
	}
	if err != nil {
		return nil, err
	}

	parsedTemplatesCache.Store(templatePath, template, nil)
	return template, nil
}
