package http

import (
	"bytes"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/corpix/uarand"
	"github.com/pkg/errors"
	"github.com/projectdiscovery/gologger"
	"github.com/projectdiscovery/nuclei/v2/pkg/output"
	"github.com/projectdiscovery/nuclei/v2/pkg/protocols"
	"github.com/projectdiscovery/nuclei/v2/pkg/protocols/common/generators"
	"github.com/projectdiscovery/nuclei/v2/pkg/protocols/common/tostring"
	"github.com/projectdiscovery/nuclei/v2/pkg/protocols/http/httpclientpool"
	"github.com/projectdiscovery/rawhttp"
	"github.com/remeh/sizedwaitgroup"
	"go.uber.org/multierr"
)

const defaultMaxWorkers = 150

// executeRaceRequest executes race condition request for a URL
func (r *Request) executeRaceRequest(reqURL string, dynamicValues, previous output.InternalEvent, callback protocols.OutputEventCallback) error {
	generator := r.newGenerator()

	maxWorkers := r.RaceNumberRequests
	swg := sizedwaitgroup.New(maxWorkers)

	var requestErr error
	mutex := &sync.Mutex{}

	request, err := generator.Make(reqURL, nil)
	if err != nil {
		return err
	}
	for i := 0; i < r.RaceNumberRequests; i++ {
		swg.Add()
		go func(httpRequest *generatedRequest) {
			err := r.executeRequest(reqURL, httpRequest, dynamicValues, previous, callback)
			mutex.Lock()
			if err != nil {
				requestErr = multierr.Append(requestErr, err)
			}
			mutex.Unlock()
			swg.Done()
		}(request)
	}
	swg.Wait()
	return requestErr
}

// executeRaceRequest executes parallel requests for a template
func (r *Request) executeParallelHTTP(reqURL string, dynamicValues, previous output.InternalEvent, callback protocols.OutputEventCallback) error {
	generator := r.newGenerator()

	// Workers that keeps enqueuing new requests
	maxWorkers := r.Threads
	swg := sizedwaitgroup.New(maxWorkers)

	var requestErr error
	mutex := &sync.Mutex{}
	for {
		request, err := generator.Make(reqURL, dynamicValues)
		if err == io.EOF {
			break
		}
		if err != nil {
			r.options.Progress.DecrementRequests(int64(generator.Total()))
			return err
		}
		swg.Add()
		go func(httpRequest *generatedRequest) {
			defer swg.Done()

			r.options.RateLimiter.Take()
			err := r.executeRequest(reqURL, httpRequest, dynamicValues, previous, callback)
			mutex.Lock()
			if err != nil {
				requestErr = multierr.Append(requestErr, err)
			}
			mutex.Unlock()
		}(request)
		r.options.Progress.IncrementRequests()
	}
	swg.Wait()
	return requestErr
}

// executeRaceRequest executes turbo http request for a URL
func (r *Request) executeTurboHTTP(reqURL string, dynamicValues, previous output.InternalEvent, callback protocols.OutputEventCallback) error {
	generator := r.newGenerator()

	// need to extract the target from the url
	URL, err := url.Parse(reqURL)
	if err != nil {
		return err
	}

	pipeOptions := rawhttp.DefaultPipelineOptions
	pipeOptions.Host = URL.Host
	pipeOptions.MaxConnections = 1
	if r.PipelineConcurrentConnections > 0 {
		pipeOptions.MaxConnections = r.PipelineConcurrentConnections
	}
	if r.PipelineRequestsPerConnection > 0 {
		pipeOptions.MaxPendingRequests = r.PipelineRequestsPerConnection
	}
	pipeclient := rawhttp.NewPipelineClient(pipeOptions)

	// defaultMaxWorkers should be a sufficient value to keep queues always full
	maxWorkers := defaultMaxWorkers
	// in case the queue is bigger increase the workers
	if pipeOptions.MaxPendingRequests > maxWorkers {
		maxWorkers = pipeOptions.MaxPendingRequests
	}
	swg := sizedwaitgroup.New(maxWorkers)

	var requestErr error
	mutex := &sync.Mutex{}
	for {
		request, err := generator.Make(reqURL, dynamicValues)
		if err == io.EOF {
			break
		}
		if err != nil {
			r.options.Progress.DecrementRequests(int64(generator.Total()))
			return err
		}
		request.pipelinedClient = pipeclient

		swg.Add()
		go func(httpRequest *generatedRequest) {
			defer swg.Done()

			err := r.executeRequest(reqURL, httpRequest, dynamicValues, previous, callback)
			mutex.Lock()
			if err != nil {
				requestErr = multierr.Append(requestErr, err)
			}
			mutex.Unlock()
		}(request)
		r.options.Progress.IncrementRequests()
	}
	swg.Wait()
	return requestErr
}

// ExecuteWithResults executes the final request on a URL
func (r *Request) ExecuteWithResults(reqURL string, dynamicValues, previous output.InternalEvent, callback protocols.OutputEventCallback) error {
	// verify if pipeline was requested
	if r.Pipeline {
		return r.executeTurboHTTP(reqURL, dynamicValues, previous, callback)
	}

	// verify if a basic race condition was requested
	if r.Race && r.RaceNumberRequests > 0 {
		return r.executeRaceRequest(reqURL, dynamicValues, previous, callback)
	}

	// verify if parallel elaboration was requested
	if r.Threads > 0 {
		return r.executeParallelHTTP(reqURL, dynamicValues, previous, callback)
	}

	generator := r.newGenerator()

	var requestErr error
	for {
		request, err := generator.Make(reqURL, dynamicValues)
		if err == io.EOF {
			break
		}
		if err != nil {
			r.options.Progress.DecrementRequests(int64(generator.Total()))
			return err
		}

		var gotOutput bool
		r.options.RateLimiter.Take()
		err = r.executeRequest(reqURL, request, dynamicValues, previous, func(event *output.InternalWrappedEvent) {
			// Add the extracts to the dynamic values if any.
			if event.OperatorsResult != nil {
				gotOutput = true
				dynamicValues = generators.MergeMaps(dynamicValues, event.OperatorsResult.DynamicValues)
			}
			callback(event)
		})
		if err != nil {
			requestErr = multierr.Append(requestErr, err)
		}
		r.options.Progress.IncrementRequests()

		if request.original.options.Options.StopAtFirstMatch && gotOutput {
			r.options.Progress.DecrementRequests(int64(generator.Total()))
			break
		}
	}
	return requestErr
}

const drainReqSize = int64(8 * 1024)

// executeRequest executes the actual generated request and returns error if occured
func (r *Request) executeRequest(reqURL string, request *generatedRequest, dynamicvalues, previous output.InternalEvent, callback protocols.OutputEventCallback) error {
	// Add User-Agent value randomly to the customHeaders slice if `random-agent` flag is given
	if r.options.Options.RandomAgent {
		r.customHeaders["User-Agent"] = uarand.GetRandom()
	}
	r.setCustomHeaders(request)

	var (
		resp      *http.Response
		fromcache bool
	)
	dumpedRequest, err := dump(request, reqURL)
	if err != nil {
		return err
	}

	if r.options.Options.Debug || r.options.Options.DebugRequests {
		gologger.Info().Msgf("[%s] Dumped HTTP request for %s\n\n", r.options.TemplateID, reqURL)
		gologger.Print().Msgf("%s", string(dumpedRequest))
	}

	var formedURL string
	var hostname string
	timeStart := time.Now()
	if request.original.Pipeline {
		formedURL = request.rawRequest.FullURL
		if parsed, err := url.Parse(formedURL); err == nil {
			hostname = parsed.Hostname()
		}
		resp, err = request.pipelinedClient.DoRaw(request.rawRequest.Method, reqURL, request.rawRequest.Path, generators.ExpandMapValues(request.rawRequest.Headers), ioutil.NopCloser(strings.NewReader(request.rawRequest.Data)))
	} else if request.original.Unsafe && request.rawRequest != nil {
		formedURL = request.rawRequest.FullURL
		if parsed, err := url.Parse(formedURL); err == nil {
			hostname = parsed.Hostname()
		}
		options := request.original.rawhttpClient.Options
		options.AutomaticContentLength = !r.DisableAutoContentLength
		options.AutomaticHostHeader = !r.DisableAutoHostname
		options.FollowRedirects = r.Redirects
		options.CustomHeaders = request.rawRequest.UnsafeHeaders
		resp, err = request.original.rawhttpClient.DoRawWithOptions(request.rawRequest.Method, reqURL, request.rawRequest.Path, generators.ExpandMapValues(request.rawRequest.Headers), ioutil.NopCloser(strings.NewReader(request.rawRequest.Data)), options)
	} else {
		hostname = request.request.URL.Hostname()
		formedURL = request.request.URL.String()
		// if nuclei-project is available check if the request was already sent previously
		if r.options.ProjectFile != nil {
			// if unavailable fail silently
			fromcache = true
			// nolint:bodyclose // false positive the response is generated at runtime
			resp, err = r.options.ProjectFile.Get(dumpedRequest)
			if err != nil {
				fromcache = false
			}
		}
		if resp == nil {
			resp, err = r.httpClient.Do(request.request)
		}
	}
	if resp == nil {
		err = errors.New("no response got for request")
	}
	if err != nil {
		// rawhttp doesn't supports draining response bodies.
		if resp != nil && resp.Body != nil && request.rawRequest == nil {
			_, _ = io.CopyN(ioutil.Discard, resp.Body, drainReqSize)
			resp.Body.Close()
		}
		r.options.Output.Request(r.options.TemplateID, reqURL, "http", err)
		r.options.Progress.DecrementRequests(1)
		return err
	}

	gologger.Verbose().Msgf("[%s] Sent HTTP request to %s", r.options.TemplateID, formedURL)
	r.options.Output.Request(r.options.TemplateID, reqURL, "http", err)

	duration := time.Since(timeStart)

	dumpedResponseHeaders, err := httputil.DumpResponse(resp, false)
	if err != nil {
		_, _ = io.CopyN(ioutil.Discard, resp.Body, drainReqSize)
		resp.Body.Close()
		return errors.Wrap(err, "could not dump http response")
	}

	var bodyReader io.Reader
	if r.MaxSize != 0 {
		bodyReader = io.LimitReader(resp.Body, int64(r.MaxSize))
	} else {
		bodyReader = resp.Body
	}
	data, err := ioutil.ReadAll(bodyReader)
	if err != nil {
		_, _ = io.CopyN(ioutil.Discard, resp.Body, drainReqSize)
		resp.Body.Close()
		return errors.Wrap(err, "could not read http body")
	}
	resp.Body.Close()

	redirectedResponse, err := dumpResponseWithRedirectChain(resp, data)
	if err != nil {
		return errors.Wrap(err, "could not read http response with redirect chain")
	}

	// net/http doesn't automatically decompress the response body if an
	// encoding has been specified by the user in the request so in case we have to
	// manually do it.
	dataOrig := data
	data, _ = handleDecompression(resp, data)

	// Dump response - step 2 - replace gzip body with deflated one or with itself (NOP operation)
	dumpedResponseBuilder := &bytes.Buffer{}
	dumpedResponseBuilder.Write(dumpedResponseHeaders)
	dumpedResponseBuilder.Write(data)
	dumpedResponse := dumpedResponseBuilder.Bytes()
	redirectedResponse = bytes.ReplaceAll(redirectedResponse, dataOrig, data)

	// Dump response - step 2 - replace gzip body with deflated one or with itself (NOP operation)
	if r.options.Options.Debug || r.options.Options.DebugResponse {
		gologger.Info().Msgf("[%s] Dumped HTTP response for %s\n\n", r.options.TemplateID, formedURL)
		gologger.Print().Msgf("%s", string(redirectedResponse))
	}

	// if nuclei-project is enabled store the response if not previously done
	if r.options.ProjectFile != nil && !fromcache {
		err := r.options.ProjectFile.Set(dumpedRequest, resp, data)
		if err != nil {
			return errors.Wrap(err, "could not store in project file")
		}
	}

	var matchedURL string
	if request.rawRequest != nil {
		matchedURL = request.rawRequest.FullURL
	}
	if request.request != nil {
		matchedURL = request.request.URL.String()
	}
	outputEvent := r.responseToDSLMap(resp, reqURL, matchedURL, tostring.UnsafeToString(dumpedRequest), tostring.UnsafeToString(dumpedResponse), tostring.UnsafeToString(data), headersToString(resp.Header), duration, request.meta)
	outputEvent["ip"] = httpclientpool.Dialer.GetDialedIP(hostname)
	outputEvent["redirect-chain"] = tostring.UnsafeToString(redirectedResponse)
	for k, v := range previous {
		outputEvent[k] = v
	}

	event := &output.InternalWrappedEvent{InternalEvent: outputEvent}
	if r.CompiledOperators != nil {
		var ok bool
		event.OperatorsResult, ok = r.CompiledOperators.Execute(outputEvent, r.Match, r.Extract)
		if ok && event.OperatorsResult != nil {
			event.OperatorsResult.PayloadValues = request.meta
			event.Results = r.MakeResultEvent(event)
		}
	}
	callback(event)
	return nil
}

const two = 2

// setCustomHeaders sets the custom headers for generated request
func (r *Request) setCustomHeaders(req *generatedRequest) {
	for k, v := range r.customHeaders {
		if req.rawRequest != nil {
			req.rawRequest.Headers[k] = v
		} else {
			req.request.Header.Set(strings.TrimSpace(k), strings.TrimSpace(v))
		}
	}
}
