package protocols

import (
	"github.com/projectdiscovery/nuclei/v2/internal/progress"
	"github.com/projectdiscovery/nuclei/v2/pkg/catalogue"
	"github.com/projectdiscovery/nuclei/v2/pkg/operators/extractors"
	"github.com/projectdiscovery/nuclei/v2/pkg/operators/matchers"
	"github.com/projectdiscovery/nuclei/v2/pkg/output"
	"github.com/projectdiscovery/nuclei/v2/pkg/projectfile"
	"github.com/projectdiscovery/nuclei/v2/pkg/types"
	"go.uber.org/ratelimit"
)

// Executer is an interface implemented any protocol based request executer.
type Executer interface {
	// Compile compiles the execution generators preparing any requests possible.
	Compile() error
	// Requests returns the total number of requests the rule will perform
	Requests() int
	// Execute executes the protocol group and returns true or false if results were found.
	Execute(input string) (bool, error)
	// ExecuteWithResults executes the protocol requests and returns results instead of writing them.
	ExecuteWithResults(input string) ([]*output.InternalWrappedEvent, error)
}

// ExecuterOptions contains the configuration options for executer clients
type ExecuterOptions struct {
	// TemplateID is the ID of the template for the request
	TemplateID string
	// TemplatePath is the path of the template for the request
	TemplatePath string
	// TemplateInfo contains information block of the template request
	TemplateInfo map[string]string
	// Output is a writer interface for writing output events from executer.
	Output output.Writer
	// Options contains configuration options for the executer.
	Options *types.Options
	// Progress is a progress client for scan reporting
	Progress *progress.Progress
	// RateLimiter is a rate-limiter for limiting sent number of requests.
	RateLimiter ratelimit.Limiter
	// Catalogue is a template catalogue implementation for nuclei
	Catalogue *catalogue.Catalogue
	// ProjectFile is the project file for nuclei
	ProjectFile *projectfile.ProjectFile
}

// Request is an interface implemented any protocol based request generator.
type Request interface {
	// Compile compiles the request generators preparing any requests possible.
	Compile(options *ExecuterOptions) error
	// Requests returns the total number of requests the rule will perform
	Requests() int
	// Match performs matching operation for a matcher on model and returns true or false.
	Match(data map[string]interface{}, matcher *matchers.Matcher) bool
	// Extract performs extracting operation for a extractor on model and returns true or false.
	Extract(data map[string]interface{}, matcher *extractors.Extractor) map[string]struct{}
	// ExecuteWithResults executes the protocol requests and returns results instead of writing them.
	ExecuteWithResults(input string, metadata output.InternalEvent) ([]*output.InternalWrappedEvent, error)
}
