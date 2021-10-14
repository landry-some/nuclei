package testutils

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// RunNucleiTemplateAndGetResults returns a list of results for a template
func RunNucleiTemplateAndGetResults(template, url string, debug bool, extra ...string) ([]string, error) {
	return RunNucleiAndGetResults(true, template, url, debug, extra...)
}

// RunNucleiWorkflowAndGetResults returns a list of results for a workflow
func RunNucleiWorkflowAndGetResults(template, url string, debug bool, extra ...string) ([]string, error) {
	return RunNucleiAndGetResults(false, template, url, debug, extra...)
}

func RunNucleiAndGetResults(isTemplate bool, template, url string, debug bool, extra ...string) ([]string, error) {
	var templateOrWorkflowFlag string
	if isTemplate {
		templateOrWorkflowFlag = "-t"
	} else {
		templateOrWorkflowFlag = "-w"
	}

	return RunNucleiBareArgsAndGetResults(debug, append([]string{
		templateOrWorkflowFlag,
		template,
		"-target",
		url,
	}, extra...)...)
}

func RunNucleiBareArgsAndGetResults(debug bool, extra ...string) ([]string, error) {
	cmd := exec.Command("./nuclei")
	if debug {
		cmd.Args = append(cmd.Args, "-debug")
		cmd.Stderr = os.Stderr
		fmt.Println(cmd.String())
	} else {
		cmd.Args = append(cmd.Args, "-silent")
	}
	cmd.Args = append(cmd.Args, extra...)
	data, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var parts []string
	items := strings.Split(string(data), "\n")
	for _, i := range items {
		if i != "" {
			parts = append(parts, i)
		}
	}
	return parts, nil
}

var templateLoaded = regexp.MustCompile(`(?:Templates|Workflows) loaded[^:]*: (\d+)`)

// RunNucleiBinaryAndGetLoadedTemplates returns a list of results for a template
func RunNucleiBinaryAndGetLoadedTemplates(nucleiBinary string, debug bool, args []string) (string, error) {
	cmd := exec.Command(nucleiBinary, args...)
	if debug {
		fmt.Println(cmd.String())
	}
	data, err := cmd.CombinedOutput()
	if err != nil {
		return "", err
	}
	matches := templateLoaded.FindAllStringSubmatch(string(data), -1)
	if len(matches) == 0 {
		return "", errors.New("no matches found")
	}
	return matches[0][1], nil
}

// TestCase is a single integration test case
type TestCase interface {
	// Execute executes a test case and returns any errors if occurred
	Execute(filePath string) error
}

// TCPServer creates a new tcp server that returns a response
type TCPServer struct {
	URL      string
	listener net.Listener
}

// NewTCPServer creates a new TCP server from a handler
func NewTCPServer(handler func(conn net.Conn)) *TCPServer {
	server := &TCPServer{}

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	server.URL = l.Addr().String()
	server.listener = l

	go func() {
		for {
			// Listen for an incoming connection.
			conn, err := l.Accept()
			if err != nil {
				continue
			}
			// Handle connections in a new goroutine.
			go handler(conn)
		}
	}()
	return server
}

// Close closes the TCP server
func (s *TCPServer) Close() {
	s.listener.Close()
}
