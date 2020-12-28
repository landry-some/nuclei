package http

import (
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/Knetic/govaluate"
	"github.com/projectdiscovery/nuclei/v2/pkg/operators/common/dsl"
	"github.com/projectdiscovery/nuclei/v2/pkg/protocols/common/generators"
	"github.com/projectdiscovery/nuclei/v2/pkg/protocols/common/replacer"
	"github.com/projectdiscovery/nuclei/v2/pkg/protocols/http/race"
	"github.com/projectdiscovery/nuclei/v2/pkg/protocols/http/raw"
	"github.com/projectdiscovery/rawhttp"
	"github.com/projectdiscovery/retryablehttp-go"
)

var (
	urlWithPortRegex        = regexp.MustCompile(`{{BaseURL}}:(\d+)`)
	templateExpressionRegex = regexp.MustCompile(`(?m)\{\{[^}]+\}\}`)
)

// requestGenerator generates requests sequentially based on various
// configurations for a http request template.
//
// If payload values are present, an iterator is created for the payload
// values. Paths and Raw requests are supported as base input, so
// it will automatically select between them based on the template.
type requestGenerator struct {
	currentIndex    int
	request         *Request
	payloadIterator *generators.Iterator
}

// newGenerator creates a new request generator instance
func (r *Request) newGenerator() *requestGenerator {
	generator := &requestGenerator{request: r}

	if len(r.Payloads) > 0 {
		generator.payloadIterator = r.generator.NewIterator()
	}
	return generator
}

// nextValue returns the next path or the next raw request depending on user input
// It returns false if all the inputs have been exhausted by the generator instance.
func (r *requestGenerator) nextValue() (string, map[string]interface{}, bool) {
	// If we have paths, return the next path.
	if len(r.request.Path) > 0 && r.currentIndex < len(r.request.Path) {
		if item := r.request.Path[r.currentIndex]; item != "" {
			r.currentIndex++
			return item, nil, true
		}
	}

	// If we have raw requests, start with the request at current index.
	// If we are not at the start, then check if the iterator for payloads
	// has finished if there are any.
	//
	// If the iterator has finished for the current raw request
	// then reset it and move on to the next value, otherwise use the last request.
	if len(r.request.Raw) > 0 && r.currentIndex < len(r.request.Raw) {
		if r.payloadIterator != nil {
			payload, ok := r.payloadIterator.Value()
			if !ok {
				r.currentIndex++
				r.payloadIterator.Reset()

				// No more payloads request for us now.
				if len(r.request.Raw) == r.currentIndex {
					return "", nil, false
				}
				if item := r.request.Raw[r.currentIndex]; item != "" {
					newPayload, ok := r.payloadIterator.Value()
					return item, newPayload, ok
				}
				return "", nil, false
			}
			return r.request.Raw[r.currentIndex], payload, true
		}
		if item := r.request.Raw[r.currentIndex]; item != "" {
			r.currentIndex++
			return item, nil, true
		}
	}
	return "", nil, false
}

// generatedRequest is a single wrapped generated request for a template request
type generatedRequest struct {
	original        *Request
	rawRequest      *raw.Request
	meta            map[string]interface{}
	pipelinedClient *rawhttp.PipelineClient
	request         *retryablehttp.Request
}

// Make creates a http request for the provided input.
// It returns io.EOF as error when all the requests have been exhausted.
func (r *requestGenerator) Make(baseURL string, dynamicValues map[string]interface{}) (*generatedRequest, error) {
	data, payloads, ok := r.nextValue()
	if !ok {
		return nil, io.EOF
	}
	ctx := context.Background()

	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}

	hostname := parsed.Host
	values := generators.MergeMaps(dynamicValues, map[string]interface{}{
		"BaseURL":  baseURLWithTemplatePrefs(data, parsed),
		"Hostname": hostname,
	})

	// If data contains \n it's a raw request, process it like that. Else
	// continue with the template based request flow.
	if strings.Contains(data, "\n") {
		return r.makeHTTPRequestFromRaw(ctx, baseURL, data, values, payloads)
	}
	return r.makeHTTPRequestFromModel(ctx, data, values)
}

// Remaining returns the remaining number of requests for the generator
func (r *requestGenerator) Remaining() int {
	if r.payloadIterator != nil {
		payloadRemaining := r.payloadIterator.Remaining()
		return (len(r.request.Raw) - r.currentIndex + 1) * payloadRemaining
	}
	return len(r.request.Path) - r.currentIndex + 1
}

// baseURLWithTemplatePrefs returns the url for BaseURL keeping
// the template port and path preference
func baseURLWithTemplatePrefs(data string, parsedURL *url.URL) string {
	// template port preference over input URL port
	// template has port
	hasPort := len(urlWithPortRegex.FindStringSubmatch(data)) > 0
	if hasPort {
		// check if also the input contains port, in this case extracts the url
		if hostname, _, err := net.SplitHostPort(parsedURL.Host); err == nil {
			parsedURL.Host = hostname
		}
	}
	return parsedURL.String()
}

// MakeHTTPRequestFromModel creates a *http.Request from a request template
func (r *requestGenerator) makeHTTPRequestFromModel(ctx context.Context, data string, values map[string]interface{}) (*generatedRequest, error) {
	URL := replacer.New(values).Replace(data)

	// Build a request on the specified URL
	req, err := http.NewRequestWithContext(ctx, r.request.Method, URL, nil)
	if err != nil {
		return nil, err
	}

	request, err := r.fillRequest(req, values)
	if err != nil {
		return nil, err
	}
	return &generatedRequest{request: request}, nil
}

// makeHTTPRequestFromRaw creates a *http.Request from a raw request
func (r *requestGenerator) makeHTTPRequestFromRaw(ctx context.Context, baseURL, data string, values, payloads map[string]interface{}) (*generatedRequest, error) {
	// Add trailing line
	data += "\n"

	// If we have payloads, handle them by evaluating them at runtime.
	if len(r.request.Payloads) > 0 {
		finalPayloads, err := r.getPayloadValues(baseURL, payloads)
		if err != nil {
			return nil, err
		}
		return r.handleRawWithPaylods(ctx, data, baseURL, values, finalPayloads)
	}
	return r.handleRawWithPaylods(ctx, data, baseURL, values, nil)
}

// handleRawWithPaylods handles raw requests along with paylaods
func (r *requestGenerator) handleRawWithPaylods(ctx context.Context, rawRequest, baseURL string, values, genValues map[string]interface{}) (*generatedRequest, error) {
	baseValues := generators.CopyMap(values)
	finValues := generators.MergeMaps(baseValues, genValues)

	// Replace the dynamic variables in the URL if any
	rawRequest = replacer.New(finValues).Replace(rawRequest)

	dynamicValues := make(map[string]interface{})
	for _, match := range templateExpressionRegex.FindAllString(rawRequest, -1) {
		// check if the match contains a dynamic variable
		expr := generators.TrimDelimiters(match)
		compiled, err := govaluate.NewEvaluableExpressionWithFunctions(expr, dsl.HelperFunctions())

		if err != nil {
			return nil, err
		}

		result, err := compiled.Evaluate(finValues)
		if err != nil {
			return nil, err
		}
		dynamicValues[expr] = result
	}

	// Replacer dynamic values if any in raw request and parse it
	rawRequest = replacer.New(dynamicValues).Replace(rawRequest)
	rawRequestData, err := raw.Parse(rawRequest, baseURL, r.request.Unsafe)
	if err != nil {
		return nil, err
	}

	// rawhttp
	if r.request.Unsafe {
		unsafeReq := &generatedRequest{
			rawRequest: rawRequestData,
			meta:       genValues,
			original:   r.request,
		}
		return unsafeReq, nil
	}

	// retryablehttp
	var body io.ReadCloser
	body = ioutil.NopCloser(strings.NewReader(rawRequestData.Data))
	if r.request.Race {
		// More or less this ensures that all requests hit the endpoint at the same approximated time
		// Todo: sync internally upon writing latest request byte
		body = race.NewOpenGateWithTimeout(body, time.Duration(2)*time.Second)
	}

	req, err := http.NewRequestWithContext(ctx, rawRequestData.Method, rawRequestData.FullURL, body)
	if err != nil {
		return nil, err
	}

	// copy headers
	for key, value := range rawRequestData.Headers {
		req.Header[key] = []string{value}
	}

	request, err := r.fillRequest(req, values)
	if err != nil {
		return nil, err
	}
	return &generatedRequest{request: request, meta: genValues}, nil
}

// fillRequest fills various headers in the request with values
func (r *requestGenerator) fillRequest(req *http.Request, values map[string]interface{}) (*retryablehttp.Request, error) {
	// Set the header values requested
	replacer := replacer.New(values)
	for header, value := range r.request.Headers {
		req.Header[header] = []string{replacer.Replace(value)}
	}

	// In case of multiple threads the underlying connection should remain open to allow reuse
	if r.request.Threads <= 0 && req.Header.Get("Connection") == "" {
		req.Close = true
	}

	// Check if the user requested a request body
	if r.request.Body != "" {
		req.Body = ioutil.NopCloser(strings.NewReader(r.request.Body))
	}
	setHeader(req, "User-Agent", "Nuclei - Open-source project (github.com/projectdiscovery/nuclei)")

	// raw requests are left untouched
	if len(r.request.Raw) > 0 {
		return retryablehttp.FromRequest(req)
	}
	setHeader(req, "Accept", "*/*")
	setHeader(req, "Accept-Language", "en")

	return retryablehttp.FromRequest(req)
}

// setHeader sets some headers only if the header wasn't supplied by the user
func setHeader(req *http.Request, name, value string) {
	if _, ok := req.Header[name]; !ok {
		req.Header.Set(name, value)
	}
}

// getPayloadValues returns current payload values for a request
func (r *requestGenerator) getPayloadValues(reqURL string, templatePayloads map[string]interface{}) (map[string]interface{}, error) {
	payloadProcessedValues := make(map[string]interface{})

	for k, v := range templatePayloads {
		kexp := v.(string)
		// if it doesn't containing markups, we just continue
		if !strings.Contains(kexp, replacer.MarkerParenthesisOpen) || strings.Contains(kexp, replacer.MarkerParenthesisClose) || strings.Contains(kexp, replacer.MarkerGeneral) {
			payloadProcessedValues[k] = v
			continue
		}
		// attempts to expand expressions
		compiled, err := govaluate.NewEvaluableExpressionWithFunctions(kexp, dsl.HelperFunctions())
		if err != nil {
			// it is a simple literal payload => proceed with literal value
			payloadProcessedValues[k] = v
			continue
		}
		// it is an expression - try to solve it
		expValue, err := compiled.Evaluate(templatePayloads)
		if err != nil {
			// an error occurred => proceed with literal value
			payloadProcessedValues[k] = v
			continue
		}
		payloadProcessedValues[k] = fmt.Sprint(expValue)
	}
	var err error
	if len(payloadProcessedValues) == 0 {
		err = ErrNoPayload
	}
	return payloadProcessedValues, err
}

// ErrNoPayload error to avoid the additional base null request
var ErrNoPayload = fmt.Errorf("no payload found")
