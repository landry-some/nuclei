package requests

import (
	"bufio"
	"context"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/Knetic/govaluate"
	"github.com/projectdiscovery/nuclei/v2/pkg/extractors"
	"github.com/projectdiscovery/nuclei/v2/pkg/generators"
	"github.com/projectdiscovery/nuclei/v2/pkg/matchers"
	retryablehttp "github.com/projectdiscovery/retryablehttp-go"
)

const (
	two   = 2
	three = 3
)

var urlWithPortRgx = regexp.MustCompile(`{{BaseURL}}:(\d+)`)
var urlWithPathRgx = regexp.MustCompile(`{{BaseURL}}.*/`)

// BulkHTTPRequest contains a request to be made from a template
type BulkHTTPRequest struct {
	// CookieReuse is an optional setting that makes cookies shared within requests
	CookieReuse bool `yaml:"cookie-reuse,omitempty"`
	// Redirects specifies whether redirects should be followed.
	Redirects bool   `yaml:"redirects,omitempty"`
	Name      string `yaml:"Name,omitempty"`
	// AttackType is the attack type
	// Sniper, PitchFork and ClusterBomb. Default is Sniper
	AttackType string `yaml:"attack,omitempty"`
	// attackType is internal attack type
	attackType generators.Type
	// Path contains the path/s for the request variables
	Payloads map[string]interface{} `yaml:"payloads,omitempty"`
	// Method is the request method, whether GET, POST, PUT, etc
	Method string `yaml:"method"`
	// Path contains the path/s for the request
	Path []string `yaml:"path"`
	// Headers contains headers to send with the request
	Headers map[string]string `yaml:"headers,omitempty"`
	// Body is an optional parameter which contains the request body for POST methods, etc
	Body string `yaml:"body,omitempty"`
	// Matchers contains the detection mechanism for the request to identify
	// whether the request was successful
	Matchers []*matchers.Matcher `yaml:"matchers,omitempty"`
	// MatchersCondition is the condition of the matchers
	// whether to use AND or OR. Default is OR.
	MatchersCondition string `yaml:"matchers-condition,omitempty"`
	// matchersCondition is internal condition for the matchers.
	matchersCondition matchers.ConditionType
	// Extractors contains the extraction mechanism for the request to identify
	// and extract parts of the response.
	Extractors []*extractors.Extractor `yaml:"extractors,omitempty"`
	// MaxRedirects is the maximum number of redirects that should be followed.
	MaxRedirects int `yaml:"max-redirects,omitempty"`
	// Raw contains raw requests
	Raw []string `yaml:"raw,omitempty"`
	// Specify in order to skip request RFC normalization
	Unsafe bool `yaml:"unsafe,omitempty"`
	// Internal Finite State Machine keeping track of scan process
	gsfm *GeneratorFSM
}

// GetMatchersCondition returns the condition for the matcher
func (r *BulkHTTPRequest) GetMatchersCondition() matchers.ConditionType {
	return r.matchersCondition
}

// SetMatchersCondition sets the condition for the matcher
func (r *BulkHTTPRequest) SetMatchersCondition(condition matchers.ConditionType) {
	r.matchersCondition = condition
}

// GetAttackType returns the attack
func (r *BulkHTTPRequest) GetAttackType() generators.Type {
	return r.attackType
}

// SetAttackType sets the attack
func (r *BulkHTTPRequest) SetAttackType(attack generators.Type) {
	r.attackType = attack
}

// GetRequestCount returns the total number of requests the YAML rule will perform
func (r *BulkHTTPRequest) GetRequestCount() int64 {
	return int64(len(r.Raw) | len(r.Path))
}

// MakeHTTPRequest makes the HTTP request
func (r *BulkHTTPRequest) MakeHTTPRequest(ctx context.Context, baseURL string, dynamicValues map[string]interface{}, data string) (*HTTPRequest, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}

	hostname := parsed.Host

	values := generators.MergeMaps(dynamicValues, map[string]interface{}{
		"BaseURL":  baseURLWithTemplatePrefs(data, parsed),
		"Hostname": hostname,
	})

	// if data contains \n it's a raw request
	if strings.Contains(data, "\n") {
		return r.makeHTTPRequestFromRaw(ctx, baseURL, data, values)
	}

	return r.makeHTTPRequestFromModel(ctx, data, values)
}

// MakeHTTPRequestFromModel creates a *http.Request from a request template
func (r *BulkHTTPRequest) makeHTTPRequestFromModel(ctx context.Context, data string, values map[string]interface{}) (*HTTPRequest, error) {
	replacer := newReplacer(values)
	URL := replacer.Replace(data)

	// Build a request on the specified URL
	req, err := http.NewRequestWithContext(ctx, r.Method, URL, nil)
	if err != nil {
		return nil, err
	}

	request, err := r.fillRequest(req, values)
	if err != nil {
		return nil, err
	}

	return &HTTPRequest{Request: request}, nil
}

// InitGenerator initializes the generator
func (r *BulkHTTPRequest) InitGenerator() {
	r.gsfm = NewGeneratorFSM(r.attackType, r.Payloads, r.Path, r.Raw)
}

// CreateGenerator creates the generator
func (r *BulkHTTPRequest) CreateGenerator(reqURL string) {
	r.gsfm.Add(reqURL)
}

// HasGenerator check if an URL has a generator
func (r *BulkHTTPRequest) HasGenerator(reqURL string) bool {
	return r.gsfm.Has(reqURL)
}

// ReadOne reads and return a generator by URL
func (r *BulkHTTPRequest) ReadOne(reqURL string) {
	r.gsfm.ReadOne(reqURL)
}

// makeHTTPRequestFromRaw creates a *http.Request from a raw request
func (r *BulkHTTPRequest) makeHTTPRequestFromRaw(ctx context.Context, baseURL, data string, values map[string]interface{}) (*HTTPRequest, error) {
	// Add trailing line
	data += "\n"

	if len(r.Payloads) > 0 {
		r.gsfm.InitOrSkip(baseURL)
		r.ReadOne(baseURL)

		return r.handleRawWithPaylods(ctx, data, baseURL, values, r.gsfm.Value(baseURL))
	}

	// otherwise continue with normal flow
	return r.handleRawWithPaylods(ctx, data, baseURL, values, nil)
}

func (r *BulkHTTPRequest) handleRawWithPaylods(ctx context.Context, raw, baseURL string, values, genValues map[string]interface{}) (*HTTPRequest, error) {
	baseValues := generators.CopyMap(values)
	finValues := generators.MergeMaps(baseValues, genValues)

	replacer := newReplacer(finValues)

	// Replace the dynamic variables in the URL if any
	raw = replacer.Replace(raw)

	dynamicValues := make(map[string]interface{})
	// find all potentials tokens between {{}}
	var re = regexp.MustCompile(`(?m)\{\{.+}}`)
	for _, match := range re.FindAllString(raw, -1) {
		// check if the match contains a dynamic variable
		expr := generators.TrimDelimiters(match)
		compiled, err := govaluate.NewEvaluableExpressionWithFunctions(expr, generators.HelperFunctions())

		if err != nil {
			return nil, err
		}

		result, err := compiled.Evaluate(finValues)
		if err != nil {
			return nil, err
		}

		dynamicValues[expr] = result
	}

	// replace dynamic values
	dynamicReplacer := newReplacer(dynamicValues)
	raw = dynamicReplacer.Replace(raw)

	rawRequest, err := r.parseRawRequest(raw, baseURL)
	if err != nil {
		return nil, err
	}

	// rawhttp
	if r.Unsafe {
		return &HTTPRequest{RawRequest: rawRequest, Meta: genValues}, nil
	}

	// retryablehttp
	req, err := http.NewRequestWithContext(ctx, rawRequest.Method, rawRequest.FullURL, strings.NewReader(rawRequest.Data))
	if err != nil {
		return nil, err
	}

	// copy headers
	for key, value := range rawRequest.Headers {
		req.Header[key] = []string{value}
	}

	request, err := r.fillRequest(req, values)
	if err != nil {
		return nil, err
	}

	return &HTTPRequest{Request: request, Meta: genValues}, nil
}

func (r *BulkHTTPRequest) fillRequest(req *http.Request, values map[string]interface{}) (*retryablehttp.Request, error) {
	setHeader(req, "Connection", "close")
	req.Close = true
	replacer := newReplacer(values)

	// Check if the user requested a request body
	if r.Body != "" {
		req.Body = ioutil.NopCloser(strings.NewReader(r.Body))
	}

	// Set the header values requested
	for header, value := range r.Headers {
		req.Header[header] = []string{replacer.Replace(value)}
	}

	setHeader(req, "User-Agent", "Nuclei - Open-source project (github.com/projectdiscovery/nuclei)")

	// raw requests are left untouched
	if len(r.Raw) > 0 {
		return retryablehttp.FromRequest(req)
	}

	setHeader(req, "Accept", "*/*")
	setHeader(req, "Accept-Language", "en")

	return retryablehttp.FromRequest(req)
}

// HTTPRequest is the basic HTTP request
type HTTPRequest struct {
	Request    *retryablehttp.Request
	RawRequest *RawRequest
	Meta       map[string]interface{}
}

func setHeader(req *http.Request, name, value string) {
	// Set some headers only if the header wasn't supplied by the user
	if _, ok := req.Header[name]; !ok {
		req.Header.Set(name, value)
	}
}

// baseURLWithTemplatePrefs returns the url for BaseURL keeping
// the template port and path preference
func baseURLWithTemplatePrefs(data string, parsedURL *url.URL) string {
	// template port preference over input URL port
	hasPort := len(urlWithPortRgx.FindStringSubmatch(data)) > 0
	if hasPort {
		hostname, _, _ := net.SplitHostPort(parsedURL.Host)
		parsedURL.Host = hostname
	}

	// template path preference over input URL path
	hasPath := len(urlWithPathRgx.FindStringSubmatch(data)) > 0
	if hasPath {
		parsedURL.Path = ""
	}

	return parsedURL.String()
}

// CustomHeaders valid for all requests
type CustomHeaders []string

// String returns just a label
func (c *CustomHeaders) String() string {
	return "Custom Global Headers"
}

// Set a new global header
func (c *CustomHeaders) Set(value string) error {
	*c = append(*c, value)
	return nil
}

// RawRequest defines a basic HTTP raw request
type RawRequest struct {
	FullURL string
	Method  string
	Path    string
	Data    string
	Headers map[string]string
}

// parseRawRequest parses the raw request as supplied by the user
func (r *BulkHTTPRequest) parseRawRequest(request, baseURL string) (*RawRequest, error) {
	reader := bufio.NewReader(strings.NewReader(request))

	rawRequest := RawRequest{
		Headers: make(map[string]string),
	}

	s, err := reader.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("could not read request: %s", err)
	}

	parts := strings.Split(s, " ")

	if len(parts) < three {
		return nil, fmt.Errorf("malformed request supplied")
	}
	// Set the request Method
	rawRequest.Method = parts[0]

	// Accepts all malformed headers
	var key, value string
	for {
		line, readErr := reader.ReadString('\n')
		line = strings.TrimSpace(line)

		if readErr != nil || line == "" {
			break
		}

		p := strings.SplitN(line, ":", two)
		key = p[0]
		if len(p) > 1 {
			value = p[1]
		}

		rawRequest.Headers[key] = value
	}

	// Handle case with the full http url in path. In that case,
	// ignore any host header that we encounter and use the path as request URL
	if strings.HasPrefix(parts[1], "http") {
		parsed, parseErr := url.Parse(parts[1])
		if parseErr != nil {
			return nil, fmt.Errorf("could not parse request URL: %s", parseErr)
		}

		rawRequest.Path = parts[1]
		rawRequest.Headers["Host"] = parsed.Host
	} else {
		rawRequest.Path = parts[1]
	}

	// If raw request doesn't have a Host header and/ path,
	// this will be generated from the parsed baseURL
	parsedURL, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("could not parse request URL: %s", err)
	}

	var hostURL string
	if rawRequest.Headers["Host"] == "" {
		hostURL = parsedURL.Host
	} else {
		hostURL = rawRequest.Headers["Host"]
	}

	if rawRequest.Path == "" {
		rawRequest.Path = parsedURL.Path
	} else if strings.HasPrefix(rawRequest.Path, "?") {
		// requests generated from http.ReadRequest have incorrect RequestURI, so they
		// cannot be used to perform another request directly, we need to generate a new one
		// with the new target url
		rawRequest.Path = fmt.Sprintf("%s%s", parsedURL.Path, rawRequest.Path)
	}

	rawRequest.FullURL = fmt.Sprintf("%s://%s%s", parsedURL.Scheme, strings.TrimSpace(hostURL), rawRequest.Path)

	// Set the request body
	b, err := ioutil.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("could not read request body: %s", err)
	}

	rawRequest.Data = string(b)

	return &rawRequest, nil
}

// Next returns the next generator by URL
func (r *BulkHTTPRequest) Next(reqURL string) bool {
	return r.gsfm.Next(reqURL)
}

// Position returns the current generator's position by URL
func (r *BulkHTTPRequest) Position(reqURL string) int {
	return r.gsfm.Position(reqURL)
}

// Reset resets the generator by URL
func (r *BulkHTTPRequest) Reset(reqURL string) {
	r.gsfm.Reset(reqURL)
}

// Current returns the current generator by URL
func (r *BulkHTTPRequest) Current(reqURL string) string {
	return r.gsfm.Current(reqURL)
}

// Total is the total number of requests
func (r *BulkHTTPRequest) Total() int {
	return len(r.Path) + len(r.Raw)
}

// Increment increments the processed request
func (r *BulkHTTPRequest) Increment(reqURL string) {
	r.gsfm.Increment(reqURL)
}
