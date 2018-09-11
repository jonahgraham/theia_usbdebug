// Layout
// Root/
//   usbdebug.exe
//   common/
//     node.exe
//     JLinkARM.dll
//     JLinkGDBServerCL.exe
//     arm-none-eabi-gdb.exe
//   dap/
//     cortex-debug/ (its package)

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
	"github.com/getlantern/deepcopy"
	"github.com/getlantern/systray"
	"github.com/getlantern/systray/example/icon"
	"github.com/gorilla/websocket"
	homedir "github.com/mitchellh/go-homedir"
	"github.com/skratchdot/open-golang/open"
	toast "gopkg.in/toast.v1"
)

var port string
var homepath string

// Configuration of USB Debug
type Configuration struct {
	AllowedOrigins map[string]bool
}

var configuration Configuration

func init() {
	homepathdefault, err := homedir.Expand("~/.usbdebug")
	if err != nil {
		log.Fatal(err)
	}
	flag.StringVar(&port, "port", "30784", "service port on localhost")
	flag.StringVar(&homepath, "home", homepathdefault, "local settings location")
}

func settingsFilename() string {
	return filepath.Join(homepath, "settings.json")
}

func newIfEmptySettingsFile() {
	if _, err := os.Stat(settingsFilename()); os.IsNotExist(err) {
		configuration = Configuration{
			AllowedOrigins: map[string]bool{
				"an example here":         true,
				"a disabled example here": false,
			},
		}
		os.MkdirAll(filepath.Dir(settingsFilename()), 0700)
		file, _ := os.Create(settingsFilename())
		defer file.Close()
		encoder := json.NewEncoder(file)
		err := encoder.Encode(&configuration)
		if err != nil {
			fmt.Println("error:", err)
		}
	}
}
func loadSettingsFile() {
	file, _ := os.Open(settingsFilename())
	defer file.Close()
	decoder := json.NewDecoder(file)
	configuration = Configuration{}
	err := decoder.Decode(&configuration)
	if err != nil {
		fmt.Println("error:", err)
	}
	fmt.Println("Settings:")
	fmt.Println(configuration)
}

func loadAndWatchSettingsFile() {
	newIfEmptySettingsFile()
	loadSettingsFile()
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatal(err)
	}
	// TODO where to close? defer watcher.Close()

	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				log.Println("event:", event)
				if event.Op&fsnotify.Write == fsnotify.Write {
					log.Println("modified file:", event.Name)
					loadSettingsFile()
				}
				if event.Op&fsnotify.Remove == fsnotify.Remove {
					log.Println("removed file:", event.Name)
					// TODO exit as config is gone
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Println("error:", err)
			}
		}
	}()

	err = watcher.Add(settingsFilename())
	if err != nil {
		log.Fatal(err)
	}
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header["Origin"]
		if len(origin) == 0 {
			log.Println("Missing origin, websocket rejected")
			permissionDeniedPrompt("<missing origin>")
			return false
		}
		if configuration.AllowedOrigins[origin[0]] {
			log.Println("Permitted origin, websocket accepted")
			permissionAllowedPrompt(origin[0])
			return true
		}
		log.Println("Unknown origin, websocket accepted")
		permissionDeniedPrompt(origin[0])
		return false
	},
	// use default options for everything else
}

func debug(w http.ResponseWriter, r *http.Request) {
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Print("upgrade:", err)
		return
	}
	defer c.Close()
	for {
		mt, message, err := c.ReadMessage()
		if err != nil {
			log.Println("read:", err)
			break
		}
		log.Printf("recv: %s", message)
		err = c.WriteMessage(mt, message)
		if err != nil {
			log.Println("write:", err)
			break
		}
	}
}

func home(w http.ResponseWriter, r *http.Request) {
	if r.URL.String() == "/" {
		log.Printf("Serving Home (" + r.URL.String() + ")\n")
		homeTemplate.Execute(w, "ws://"+r.Host+"/debug")
	} else {
		log.Printf("Serving 404 Not Found (" + r.URL.String() + ")\n")
		http.NotFound(w, r)
	}
}

func help(w http.ResponseWriter, r *http.Request) {
	log.Printf("Serving Help (" + r.URL.String() + ")\n")
	v := r.URL.Query()
	origin := v.Get("origin")
	if origin != "" {
		exampleConfig := Configuration{}
		deepcopy.Copy(&exampleConfig, &configuration)
		exampleConfig.AllowedOrigins[origin] = true
		exampleJSONBytes, _ := json.Marshal(exampleConfig)
		exampleJSON := string(exampleJSONBytes)
		log.Println("Example JSON to use: " + exampleJSON)
		t := helpEnableOriginFields{
			ExampleSettings:  exampleJSON,
			Origin:           origin,
			SettingsFilename: settingsFilename(),
		}

		err := helpEnableOriginTemplate.Execute(w, t)
		if err != nil {
			panic(err)
		}
	} else {
		// TODO provide other help??
		helpTemplate.Execute(w, "ws://"+r.Host+"/debug")
	}
}

func permissionDeniedPrompt(remote string) {
	u, err := url.Parse(fmt.Sprintf(`http://localhost:%s/help`, port))
	if err != nil {
		panic("Failed to parse?")
	}
	parameters := url.Values{}
	parameters.Add("origin", remote)
	u.RawQuery = parameters.Encode()

	notification := toast.Notification{
		AppID:   "{1AC14E77-02E7-4E5D-B744-2EB1AE5198B7}\\WindowsPowerShell\\v1.0\\powershell.exe",
		Title:   "USB Debug Connection Denied",
		Message: "A USB debug connection has been initiated from " + remote + " which is not in the allowed list and therefore the debug session was denied.",
		Actions: []toast.Action{
			{Type: "protocol", Label: "Help", Arguments: u.String()},
		},
	}
	err = notification.Push()
	if err != nil {
		log.Fatalln(err)
	}
}

func permissionAllowedPrompt(remote string) {
	notification := toast.Notification{
		AppID:   "{1AC14E77-02E7-4E5D-B744-2EB1AE5198B7}\\WindowsPowerShell\\v1.0\\powershell.exe",
		Title:   "USB Debug Connection Allowed",
		Message: "A USB debug connection has been initiated from " + remote + " which is in the allowed list and therefore has been allowed.",
		Actions: []toast.Action{},
	}
	err := notification.Push()
	if err != nil {
		log.Fatalln(err)
	}
}

func onReady() {
	systray.SetIcon(icon.Data)
	systray.SetTitle("USB Debug")
	systray.SetTooltip("USB Debug")
	mStatus := systray.AddMenuItem("Status", "Open Status webpage")
	systray.AddSeparator()
	mExit := systray.AddMenuItem("Exit", "Exit USB Debug")
	go func() {
		for {
			select {
			case <-mExit.ClickedCh:
				systray.Quit()
			case <-mStatus.ClickedCh:
				open.Run(fmt.Sprintf(`http://localhost:%s/`, port))
			}
		}
	}()

	log.Println("About to start serving on " + fmt.Sprintf(`localhost:%s`, port))
	log.Fatal(http.ListenAndServe(fmt.Sprintf(`localhost:%s`, port), nil))
}

func main() {
	flag.Parse()
	log.SetFlags(0)
	loadAndWatchSettingsFile()
	http.HandleFunc("/", home)
	http.HandleFunc("/services/debug-adapter/", debug)
	http.HandleFunc("/help", help)

	onExit := func() {
		log.Printf("USB Debug Exiting\n")
	}
	// Should be called at the very beginning of main().
	systray.Run(onReady, onExit)
}

var homeTemplate = template.Must(template.New("").Parse(`
<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<script>  
window.addEventListener("load", function(evt) {

    var output = document.getElementById("output");
    var input = document.getElementById("input");
    var ws;

    var print = function(message) {
        var d = document.createElement("div");
        d.innerHTML = message;
        output.appendChild(d);
    };

    document.getElementById("open").onclick = function(evt) {
        if (ws) {
            return false;
        }
        ws = new WebSocket("{{.}}");
        ws.onopen = function(evt) {
            print("OPEN");
        }
        ws.onclose = function(evt) {
            print("CLOSE");
            ws = null;
        }
        ws.onmessage = function(evt) {
            print("RESPONSE: " + evt.data);
        }
        ws.onerror = function(evt) {
            print("ERROR: " + evt.data);
        }
        return false;
    };

    document.getElementById("send").onclick = function(evt) {
        if (!ws) {
            return false;
        }
        print("SEND: " + input.value);
        ws.send(input.value);
        return false;
    };

    document.getElementById("close").onclick = function(evt) {
        if (!ws) {
            return false;
        }
        ws.close();
        return false;
    };

});
</script>
</head>
<body>
<table>
<tr><td valign="top" width="50%">
<p>Click "Open" to create a connection to the server, 
"Send" to send a message to the server and "Close" to close the connection. 
You can change the message and send multiple times.
<p>
<form>
<button id="open">Open</button>
<button id="close">Close</button>
<p><input id="input" type="text" value="Hello world!">
<button id="send">Send</button>
</form>
</td><td valign="top" width="50%">
<div id="output"></div>
</td></tr></table>
</body>
</html>
`))

var helpTemplate = template.Must(template.New("").Parse(`
<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
</head>
<body>
Write some more help here.
</body>
</html>
`))

type helpEnableOriginFields struct {
	Origin           string
	ExampleSettings  string
	SettingsFilename string
}

var helpEnableOriginTemplate = template.Must(template.New("").Parse(`
<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
</head>
<body>
To add <pre>{{.Origin}}</pre> to the permitted sites, please add it to the settings file in <pre>{{.SettingsFilename}}</pre>
This is an example of the whole file with the new setting.
<pre>
{{.ExampleSettings}}
</pre>
</body>
</html>
`))
