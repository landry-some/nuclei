package executor

import (
	"strings"

	"github.com/projectdiscovery/gologger"
	"github.com/projectdiscovery/nuclei/pkg/matchers"
	"github.com/projectdiscovery/retryablehttp-go"
)

// writeOutputHTTP writes http output to streams
func (e *HTTPExecutor) writeOutputHTTP(req *retryablehttp.Request, matcher *matchers.Matcher, extractorResults []string) {
	builder := &strings.Builder{}

	builder.WriteRune('[')
	builder.WriteString(e.template.ID)
	if matcher != nil && len(matcher.Name) > 0 {
		builder.WriteString(":")
		builder.WriteString(matcher.Name)
	}
	builder.WriteString("] [http] ")

	// Escape the URL by replacing all % with %%
	URL := req.URL.String()
	escapedURL := strings.Replace(URL, "%", "%%", -1)
	builder.WriteString(escapedURL)

	// If any extractors, write the results
	if len(extractorResults) > 0 {
		builder.WriteString(" [")
		for i, result := range extractorResults {
			builder.WriteString(result)
			if i != len(extractorResults)-1 {
				builder.WriteRune(',')
			}
		}
		builder.WriteString("]")
	}
	builder.WriteRune('\n')

	// Write output to screen as well as any output file
	message := builder.String()
	gologger.Silentf("%s", message)

	if e.writer != nil {
		e.outputMutex.Lock()
		e.writer.WriteString(message)
		e.outputMutex.Unlock()
	}
}
