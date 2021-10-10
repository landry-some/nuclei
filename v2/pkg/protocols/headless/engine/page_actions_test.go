package engine

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/projectdiscovery/nuclei/v2/pkg/protocols/common/protocolstate"
	"github.com/projectdiscovery/nuclei/v2/pkg/types"
	"github.com/stretchr/testify/require"
)

func setUp(t *testing.T) (*Browser, *Instance, error) {
	t.Helper()
	_ = protocolstate.Init(&types.Options{})

	browser, err := New(&types.Options{ShowBrowser: false})
	require.Nil(t, err, "could not create browser")

	instance, err := browser.NewInstance()
	return browser, instance, err
}

func TestActionNavigate(t *testing.T) {
	browser, instance, err := setUp(t)
	defer browser.Close()
	defer instance.Close()
	require.Nil(t, err, "could not create browser instance")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `
		<html>
		<head>
			<title>Nuclei Test Page</title>
		</head>
		<body>
			<h1>Nuclei Test</h1>
		</body>
	</html>`)
	}))
	defer ts.Close()

	parsed, err := url.Parse(ts.URL)
	require.Nil(t, err, "could not parse URL")

	actions := []*Action{{ActionType: "navigate", Data: map[string]string{"url": "{{BaseURL}}"}}, {ActionType: "waitload"}}
	_, page, err := instance.Run(parsed, actions, 20*time.Second)
	require.Nil(t, err, "could not run page actions")
	defer page.Close()

	require.Equal(t, "Nuclei Test Page", page.Page().MustInfo().Title, "could not navigate correctly")
}

func TestActionScript(t *testing.T) {
	browser, instance, err := setUp(t)
	defer browser.Close()
	defer instance.Close()
	require.Nil(t, err, "could not create browser instance")

	t.Run("run-and-results", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintln(w, `
			<html>
			<head>
				<title>Nuclei Test Page</title>
			</head>
			<body>Nuclei Test Page</body>
			<script>window.test = 'some-data';</script>
		</html>`)
		}))
		defer ts.Close()

		parsed, err := url.Parse(ts.URL)
		require.Nil(t, err, "could not parse URL")

		actions := []*Action{
			{ActionType: "navigate", Data: map[string]string{"url": "{{BaseURL}}"}},
			{ActionType: "waitload"},
			{ActionType: "script", Name: "test", Data: map[string]string{"code": "window.test"}},
		}
		out, page, err := instance.Run(parsed, actions, 20*time.Second)
		require.Nil(t, err, "could not run page actions")
		defer page.Close()

		require.Equal(t, "Nuclei Test Page", page.Page().MustInfo().Title, "could not navigate correctly")
		require.Equal(t, "some-data", out["test"], "could not run js and get results correctly")
	})

	t.Run("hook", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintln(w, `
			<html>
			<head>
				<title>Nuclei Test Page</title>
			</head>
			<body>Nuclei Test Page</body>
		</html>`)
		}))
		defer ts.Close()

		parsed, err := url.Parse(ts.URL)
		require.Nil(t, err, "could not parse URL")

		actions := []*Action{
			{ActionType: "script", Data: map[string]string{"code": "window.test = 'some-data';", "hook": "true"}},
			{ActionType: "navigate", Data: map[string]string{"url": "{{BaseURL}}"}},
			{ActionType: "waitload"},
			{ActionType: "script", Name: "test", Data: map[string]string{"code": "window.test"}},
		}
		out, page, err := instance.Run(parsed, actions, 20*time.Second)
		require.Nil(t, err, "could not run page actions")
		defer page.Close()

		require.Equal(t, "Nuclei Test Page", page.Page().MustInfo().Title, "could not navigate correctly")
		require.Equal(t, "some-data", out["test"], "could not run js and get results correctly with js hook")
	})
}

func TestActionClick(t *testing.T) {
	browser, instance, err := setUp(t)
	defer browser.Close()
	defer instance.Close()
	require.Nil(t, err, "could not create browser instance")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `
		<html>
		<head>
			<title>Nuclei Test Page</title>
		</head>
		<body>Nuclei Test Page</body>
		<button onclick='this.setAttribute("a", "ok")'>click me</button>
	</html>`)
	}))
	defer ts.Close()

	parsed, err := url.Parse(ts.URL)
	require.Nil(t, err, "could not parse URL")

	actions := []*Action{
		{ActionType: "navigate", Data: map[string]string{"url": "{{BaseURL}}"}},
		{ActionType: "waitload"},
		{ActionType: "click", Data: map[string]string{"selector": "button"}}, // Use css selector for clicking
	}
	_, page, err := instance.Run(parsed, actions, 20*time.Second)
	require.Nil(t, err, "could not run page actions")
	defer page.Close()

	require.Equal(t, "Nuclei Test Page", page.Page().MustInfo().Title, "could not navigate correctly")
	el := page.Page().MustElement("button")
	val := el.MustAttribute("a")
	require.Equal(t, "ok", *val, "could not click button")
}

func TestActionRightClick(t *testing.T) {
	browser, instance, err := setUp(t)
	defer browser.Close()
	defer instance.Close()
	require.Nil(t, err, "could not create browser instance")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `
		<html>
		<head>
			<title>Nuclei Test Page</title>
		</head>
		<body>Nuclei Test Page</body>
		<button id="test" onrightclick=''>click me</button>
		<script>
		elm = document.getElementById("test");
		elm.onmousedown = function(event) {
			if (event.which == 3) {
				elm.setAttribute("a", "ok")
			}
		}
		</script>
	</html>`)
	}))
	defer ts.Close()

	parsed, err := url.Parse(ts.URL)
	require.Nil(t, err, "could not parse URL")

	actions := []*Action{
		{ActionType: "navigate", Data: map[string]string{"url": "{{BaseURL}}"}},
		{ActionType: "waitload"},
		{ActionType: "rightclick", Data: map[string]string{"selector": "button"}}, // Use css selector for clicking
	}
	_, page, err := instance.Run(parsed, actions, 20*time.Second)
	require.Nil(t, err, "could not run page actions")
	defer page.Close()

	require.Equal(t, "Nuclei Test Page", page.Page().MustInfo().Title, "could not navigate correctly")
	el := page.Page().MustElement("button")
	val := el.MustAttribute("a")
	require.Equal(t, "ok", *val, "could not click button")
}

func TestActionTextInput(t *testing.T) {
	browser, instance, err := setUp(t)
	defer browser.Close()
	defer instance.Close()
	require.Nil(t, err, "could not create browser instance")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `
		<html>
		<head>
			<title>Nuclei Test Page</title>
		</head>
		<body>Nuclei Test Page</body>
		<input type="text" onchange="this.setAttribute('event', 'input-change')">
	</html>`)
	}))
	defer ts.Close()

	parsed, err := url.Parse(ts.URL)
	require.Nil(t, err, "could not parse URL")

	actions := []*Action{
		{ActionType: "navigate", Data: map[string]string{"url": "{{BaseURL}}"}},
		{ActionType: "waitload"},
		{ActionType: "text", Data: map[string]string{"selector": "input", "value": "test"}},
	}
	_, page, err := instance.Run(parsed, actions, 20*time.Second)
	require.Nil(t, err, "could not run page actions")
	defer page.Close()

	require.Equal(t, "Nuclei Test Page", page.Page().MustInfo().Title, "could not navigate correctly")
	el := page.Page().MustElement("input")
	val := el.MustAttribute("event")
	require.Equal(t, "input-change", *val, "could not get input change")
	require.Equal(t, "test", el.MustText(), "could not get input change value")
}

func TestActionHeadersChange(t *testing.T) {
	browser, instance, err := setUp(t)
	defer browser.Close()
	defer instance.Close()
	require.Nil(t, err, "could not create browser instance")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Test") == "Hello" {
			fmt.Fprintln(w, `found`)
		}
	}))
	defer ts.Close()

	parsed, err := url.Parse(ts.URL)
	require.Nil(t, err, "could not parse URL")

	actions := []*Action{
		{ActionType: "setheader", Data: map[string]string{"part": "request", "key": "Test", "value": "Hello"}},
		{ActionType: "navigate", Data: map[string]string{"url": "{{BaseURL}}"}},
		{ActionType: "waitload"},
	}
	_, page, err := instance.Run(parsed, actions, 20*time.Second)
	require.Nil(t, err, "could not run page actions")
	defer page.Close()

	require.Equal(t, "found", strings.ToLower(strings.TrimSpace(page.Page().MustElement("html").MustText())), "could not set header correctly")
}

func TestActionScreenshot(t *testing.T) {
	browser, instance, err := setUp(t)
	defer browser.Close()
	defer instance.Close()
	require.Nil(t, err, "could not create browser instance")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `
		<html>
		<head>
			<title>Nuclei Test Page</title>
		</head>
		<body>Nuclei Test Page</body>
	</html>`)
	}))
	defer ts.Close()

	parsed, err := url.Parse(ts.URL)
	require.Nil(t, err, "could not parse URL")

	actions := []*Action{
		{ActionType: "navigate", Data: map[string]string{"url": "{{BaseURL}}"}},
		{ActionType: "waitload"},
		{ActionType: "screenshot", Data: map[string]string{"to": "test"}},
	}
	_, page, err := instance.Run(parsed, actions, 20*time.Second)
	require.Nil(t, err, "could not run page actions")
	defer page.Close()

	require.Equal(t, "Nuclei Test Page", page.Page().MustInfo().Title, "could not navigate correctly")
	el := page.Page()
	require.FileExists(t, "test.png", el, "could not get screenshot file")
	os.Remove("test.png")
}

func TestActionTimeInput(t *testing.T) {
	browser, instance, err := setUp(t)
	defer browser.Close()
	defer instance.Close()
	require.Nil(t, err, "could not create browser instance")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `
		<html>
		<head>
			<title>Nuclei Test Page</title>
		</head>
		<body>Nuclei Test Page</body>
		<input type="date">
	</html>`)
	}))
	defer ts.Close()

	parsed, err := url.Parse(ts.URL)
	require.Nil(t, err, "could not parse URL")

	actions := []*Action{
		{ActionType: "navigate", Data: map[string]string{"url": "{{BaseURL}}"}},
		{ActionType: "waitload"},
		{ActionType: "time", Data: map[string]string{"selector": "input", "value": "2006-01-02T15:04:05Z"}},
	}
	_, page, err := instance.Run(parsed, actions, 20*time.Second)
	require.Nil(t, err, "could not run page actions")
	defer page.Close()

	require.Equal(t, "Nuclei Test Page", page.Page().MustInfo().Title, "could not navigate correctly")
	el := page.Page().MustElement("input")
	require.Equal(t, "2006-01-02", el.MustText(), "could not get input time value")
}

func TestActionSelectInput(t *testing.T) {
	browser, instance, err := setUp(t)
	defer browser.Close()
	defer instance.Close()
	require.Nil(t, err, "could not create browser instance")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `
		<html>
			<head>
				<title>Nuclei Test Page</title>
			</head>
			<body>
				<select name="test" id="test">
				  <option value="test1">Test1</option>
				  <option value="test2">Test2</option>
				</select>
			</body>
		</html>`)
	}))
	defer ts.Close()

	parsed, err := url.Parse(ts.URL)
	require.Nil(t, err, "could not parse URL")

	actions := []*Action{
		{ActionType: "navigate", Data: map[string]string{"url": "{{BaseURL}}"}},
		{ActionType: "waitload"},
		{ActionType: "select", Data: map[string]string{"by": "x", "xpath": "//select[@id='test']", "value": "Test2", "selected": "true"}},
	}
	_, page, err := instance.Run(parsed, actions, 20*time.Second)
	require.Nil(t, err, "could not run page actions")
	defer page.Close()

	el := page.Page().MustElement("select")
	require.Equal(t, "Test2", el.MustText(), "could not get input change value")
}

func TestActionFilesInput(t *testing.T) {
	browser, instance, err := setUp(t)
	defer browser.Close()
	defer instance.Close()
	require.Nil(t, err, "could not create browser instance")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `
		<html>
		<head>
			<title>Nuclei Test Page</title>
		</head>
		<body>Nuclei Test Page</body>
		<input type="file">
	</html>`)
	}))
	defer ts.Close()

	parsed, err := url.Parse(ts.URL)
	require.Nil(t, err, "could not parse URL")

	actions := []*Action{
		{ActionType: "navigate", Data: map[string]string{"url": "{{BaseURL}}"}},
		{ActionType: "waitload"},
		{ActionType: "files", Data: map[string]string{"selector": "input", "value": "test1.pdf"}},
	}
	_, page, err := instance.Run(parsed, actions, 20*time.Second)
	require.Nil(t, err, "could not run page actions")
	defer page.Close()

	require.Equal(t, "Nuclei Test Page", page.Page().MustInfo().Title, "could not navigate correctly")
	el := page.Page().MustElement("input")
	require.Equal(t, "C:\\fakepath\\test1.pdf", el.MustText(), "could not get input file")
}

func TestActionWaitLoad(t *testing.T) {
	_ = protocolstate.Init(&types.Options{})

	browser, err := New(&types.Options{ShowBrowser: false})
	require.Nil(t, err, "could not create browser")
	defer browser.Close()

	instance, err := browser.NewInstance()
	require.Nil(t, err, "could not create browser instance")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `
		<html>
			<head>
				<title>Nuclei Test Page</title>
			</head>
			<button id="test">Wait for me!</button>
			<script>
				window.onload = () => document.querySelector('#test').style.color = 'red';
			</script>
		</html>`)
	}))
	defer ts.Close()

	parsed, err := url.Parse(ts.URL)
	require.Nil(t, err, "could not parse URL")

	actions := []*Action{
		{ActionType: "navigate", Data: map[string]string{"url": "{{BaseURL}}"}},
		{ActionType: "waitload"},
	}
	_, page, err := instance.Run(parsed, actions, 20*time.Second)
	require.Nil(t, err, "could not run page actions")
	defer page.Close()

	el := page.Page().MustElement("button")
	style, err := el.Attribute("style")
	require.Equal(t, "color: red;", string(*style), "could not get color")
}

func TestActionGetResource(t *testing.T) {
	_ = protocolstate.Init(&types.Options{})

	browser, err := New(&types.Options{ShowBrowser: false})
	require.Nil(t, err, "could not create browser")
	defer browser.Close()

	instance, err := browser.NewInstance()
	require.Nil(t, err, "could not create browser instance")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `
		<html>
			<head>
				<title>Nuclei Test Page</title>
			</head>
			<body>
				<img id="test" src="https://nuclei.projectdiscovery.io/static/logo.png">
			</body>
		</html>`)
	}))
	defer ts.Close()

	parsed, err := url.Parse(ts.URL)
	require.Nil(t, err, "could not parse URL")

	actions := []*Action{
		{ActionType: "navigate", Data: map[string]string{"url": "{{BaseURL}}"}},
		{ActionType: "getresource", Data: map[string]string{"by": "x", "xpath": "//img[@id='test']"}, Name: "src"},
	}
	out, page, err := instance.Run(parsed, actions, 20*time.Second)
	require.Nil(t, err, "could not run page actions")
	defer page.Close()

	require.Equal(t, len(out["src"]), 3159, "could not find resource")
}

func TestActionExtract(t *testing.T) {
	_ = protocolstate.Init(&types.Options{})

	browser, err := New(&types.Options{ShowBrowser: false})
	require.Nil(t, err, "could not create browser")
	defer browser.Close()

	instance, err := browser.NewInstance()
	require.Nil(t, err, "could not create browser instance")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `
		<html>
			<head>
				<title>Nuclei Test Page</title>
			</head>
			<button id="test">Wait for me!</button>
		</html>`)
	}))
	defer ts.Close()

	parsed, err := url.Parse(ts.URL)
	require.Nil(t, err, "could not parse URL")

	actions := []*Action{
		{ActionType: "navigate", Data: map[string]string{"url": "{{BaseURL}}"}},
		{ActionType: "extract", Data: map[string]string{"by": "x", "xpath": "//button[@id='test']"}, Name: "extract"},
	}
	out, page, err := instance.Run(parsed, actions, 20*time.Second)
	require.Nil(t, err, "could not run page actions")
	defer page.Close()

	require.Equal(t, "Wait for me!", out["extract"], "could not extract text")
}

func TestActionSetMethod(t *testing.T) {
	_ = protocolstate.Init(&types.Options{})

	browser, err := New(&types.Options{ShowBrowser: false})
	require.Nil(t, err, "could not create browser")
	defer browser.Close()

	instance, err := browser.NewInstance()
	require.Nil(t, err, "could not create browser instance")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `
		<html>
			<head>
				<title>Nuclei Test Page</title>
			</head>
		</html>`)
	}))
	defer ts.Close()

	parsed, err := url.Parse(ts.URL)
	require.Nil(t, err, "could not parse URL")

	actions := []*Action{
		{ActionType: "navigate", Data: map[string]string{"url": "{{BaseURL}}"}},
		{ActionType: "setmethod", Data: map[string]string{"part": "x", "method": "SET"}},
	}
	_, page, err := instance.Run(parsed, actions, 20*time.Second)
	require.Nil(t, err, "could not run page actions")
	defer page.Close()

	require.Equal(t, "SET", page.rules[0].Args["method"], "could not find resource")
}

func TestActionAddHeader(t *testing.T) {
	browser, instance, err := setUp(t)
	defer browser.Close()
	defer instance.Close()
	require.Nil(t, err, "could not create browser instance")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Test") == "Hello" {
			fmt.Fprintln(w, `found`)
		}
	}))
	defer ts.Close()

	parsed, err := url.Parse(ts.URL)
	require.Nil(t, err, "could not parse URL")

	actions := []*Action{
		{ActionType: "addheader", Data: map[string]string{"part": "request", "key": "Test", "value": "Hello"}},
		{ActionType: "navigate", Data: map[string]string{"url": "{{BaseURL}}"}},
		{ActionType: "waitload"},
	}
	_, page, err := instance.Run(parsed, actions, 20*time.Second)
	require.Nil(t, err, "could not run page actions")
	defer page.Close()

	require.Equal(t, "found", strings.ToLower(strings.TrimSpace(page.Page().MustElement("html").MustText())), "could not set header correctly")
}

func TestActionDeleteHeader(t *testing.T) {
	browser, instance, err := setUp(t)
	defer browser.Close()
	defer instance.Close()
	require.Nil(t, err, "could not create browser instance")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Test1") == "Hello" && r.Header.Get("Test2") == "" {
			fmt.Println(r.Header)
			fmt.Fprintln(w, `header deleted`)
		}
	}))
	defer ts.Close()

	parsed, err := url.Parse(ts.URL)
	require.Nil(t, err, "could not parse URL")

	actions := []*Action{
		{ActionType: "addheader", Data: map[string]string{"part": "request", "key": "Test1", "value": "Hello"}},
		{ActionType: "addheader", Data: map[string]string{"part": "request", "key": "Test2", "value": "World"}},
		{ActionType: "deleteheader", Data: map[string]string{"part": "request", "key": "Test2"}},
		{ActionType: "navigate", Data: map[string]string{"url": "{{BaseURL}}"}},
		{ActionType: "waitload"},
	}
	_, page, err := instance.Run(parsed, actions, 20*time.Second)
	require.Nil(t, err, "could not run page actions")
	defer page.Close()

	require.Equal(t, "header deleted", strings.ToLower(strings.TrimSpace(page.Page().MustElement("html").MustText())), "could not delete header correctly")
}

func TestActionSetBody(t *testing.T) {
	browser, instance, err := setUp(t)
	defer browser.Close()
	defer instance.Close()
	require.Nil(t, err, "could not create browser instance")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := ioutil.ReadAll(r.Body)
		fmt.Fprintln(w, string(body))
	}))
	defer ts.Close()

	parsed, err := url.Parse(ts.URL)
	require.Nil(t, err, "could not parse URL")

	actions := []*Action{
		{ActionType: "setbody", Data: map[string]string{"part": "request", "body": "hello"}},
		{ActionType: "navigate", Data: map[string]string{"url": "{{BaseURL}}"}},
		{ActionType: "waitload"},
	}
	_, page, err := instance.Run(parsed, actions, 20*time.Second)
	require.Nil(t, err, "could not run page actions")
	defer page.Close()

	require.Equal(t, "hello", strings.ToLower(strings.TrimSpace(page.Page().MustElement("html").MustText())), "could not set header correctly")
}

func TestActionWaitEvent(t *testing.T) {
}

func TestActionKeyboard(t *testing.T) {
	browser, instance, err := setUp(t)
	defer browser.Close()
	defer instance.Close()
	require.Nil(t, err, "could not create browser instance")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `
		<html>
			<head>
				<title>Nuclei Test Page</title>
			</head>
			<body>
				<select name="test" id="test">
				  <option value="test1">Test1</option>
				  <option value="test2">Test2</option>
				</select>
			</body>
		</html>`)
	}))
	defer ts.Close()

	parsed, err := url.Parse(ts.URL)
	require.Nil(t, err, "could not parse URL")

	actions := []*Action{
		{ActionType: "navigate", Data: map[string]string{"url": "{{BaseURL}}"}},
		{ActionType: "waitload"},
		{ActionType: "click", Data: map[string]string{"selector": "select"}},
		{ActionType: "keyboard", Data: map[string]string{"keys": "Test 2"}},
		{ActionType: "keyboard", Data: map[string]string{"keys": "enter"}},
	}
	_, page, err := instance.Run(parsed, actions, 20*time.Second)
	require.Nil(t, err, "could not run page actions")
	defer page.Close()

	el := page.Page().MustElement("select")
	require.Equal(t, "Test2", el.MustText(), "could not get input change value")
}

func TestActionDebug(t *testing.T) {
}

func TestActionSleep(t *testing.T) {
}

func TestActionWaitVisible(t *testing.T) {
	t.Run("wait for an element being visible", func(t *testing.T) {
		_ = protocolstate.Init(&types.Options{})

		browser, err := New(&types.Options{ShowBrowser: false})
		require.Nil(t, err, "could not create browser")
		defer browser.Close()

		instance, err := browser.NewInstance()
		require.Nil(t, err, "could not create browser instance")

		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintln(w, `
		<html>
			<head>
				<title>Nuclei Test Page</title>
			</head>
			<button style="display:none" id="test">Wait for me!</button>
			<script>
				setTimeout(() => document.querySelector('#test').style.display = '', 1000);
			</script>
		</html>`)
		}))
		defer ts.Close()

		parsed, err := url.Parse(ts.URL)
		require.Nil(t, err, "could not parse URL")

		actions := []*Action{
			{ActionType: "navigate", Data: map[string]string{"url": "{{BaseURL}}"}},
			{ActionType: "waitvisible", Data: map[string]string{"by": "x", "xpath": "//button[@id='test']"}},
		}
		_, page, err := instance.Run(parsed, actions, 20*time.Second)
		require.Nil(t, err, "could not run page actions")
		defer page.Close()

		page.Page().MustElement("button").MustVisible()
	})

	t.Run("timeout because of element not visible", func(t *testing.T) {
		_ = protocolstate.Init(&types.Options{})

		browser, err := New(&types.Options{ShowBrowser: false})
		require.Nil(t, err, "could not create browser")
		defer browser.Close()

		instance, err := browser.NewInstance()
		require.Nil(t, err, "could not create browser instance")

		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintln(w, `
		<html>
			<head>
				<title>Nuclei Test Page</title>
			</head>
			<button style="display:none" id="test">Wait for me!</button>
		</html>`)
		}))
		defer ts.Close()

		parsed, err := url.Parse(ts.URL)
		require.Nil(t, err, "could not parse URL")

		actions := []*Action{
			{ActionType: "navigate", Data: map[string]string{"url": "{{BaseURL}}"}},
			{ActionType: "waitvisible", Data: map[string]string{"by": "x", "xpath": "//button[@id='test']"}},
		}
		_, _, err = instance.Run(parsed, actions, 2*time.Second)
		require.Error(t, err)
		require.Contains(t, err.Error(), "could not wait element")
	})
}
