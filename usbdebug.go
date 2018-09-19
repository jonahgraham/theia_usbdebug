/********************************************************************************
 * Copyright (C) 2018 Kichwa Coders
 *
 * This program and the accompanying materials are made available under the
 * terms of the Eclipse Public License v. 2.0 which is available at
 * http://www.eclipse.org/legal/epl-2.0.
 *
 * This Source Code may also be made available under the following Secondary
 * Licenses when the conditions for such availability set forth in the Eclipse
 * Public License v. 2.0 are satisfied: GNU General Public License, version 2
 * with the GNU Classpath Exception which is available at
 * https://www.gnu.org/software/classpath/license.html.
 *
 * SPDX-License-Identifier: EPL-2.0 OR GPL-2.0 WITH Classpath-exception-2.0
 ********************************************************************************/

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/fsnotify/fsnotify"
	"github.com/getlantern/deepcopy"
	"github.com/getlantern/systray"
	"github.com/getlantern/systray/example/icon"
	"github.com/gorilla/websocket"
	homedir "github.com/mitchellh/go-homedir"
	"github.com/skratchdot/open-golang/open"
)

var port string
var homepath string

// Configuration of USB Debug
type Configuration struct {
	AllowedOrigins  map[string]bool
	DebugServerMode bool
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
			DebugServerMode: false,
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
			PermissionDeniedPrompt("<missing origin>")
			return false
		}
		if configuration.AllowedOrigins[origin[0]] {
			log.Println("Permitted origin, websocket accepted")
			PermissionAllowedPrompt(origin[0])
			return true
		}
		log.Println("Unknown origin, websocket accepted")
		PermissionDeniedPrompt(origin[0])
		return false
	},
	// use default options for everything else
}

func debug(w http.ResponseWriter, r *http.Request) {
	wsConn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Print("upgrade:", err)
		return
	}

	var toDap io.WriteCloser
	var fromDap io.ReadCloser
	// TODO make this condition based on debugServer configuration entry in the launch arguments?
	if configuration.DebugServerMode {
		dapConn, err := net.Dial("tcp", "localhost:4711")
		toDap = dapConn
		fromDap = dapConn
		if err != nil {
			log.Print("failed to connect to debugServer:", err)
			wsConn.Close()
			return
		}
	} else {
		debugServer := exec.Command(filepath.Join(homepath, "node.exe"), filepath.Join(homepath, "extension/out/src/gdb.js"))
		toDap, err = debugServer.StdinPipe()
		if err != nil {
			log.Print("failed to get debug server stdin:", err)
			wsConn.Close()
			return
		}
		fromDap, err = debugServer.StdoutPipe()
		if err != nil {
			log.Print("failed to get debug server stdout:", err)
			wsConn.Close()
			return
		}
		err = debugServer.Start()
		if err != nil {
			log.Print("failed to start debug server:", err)
			wsConn.Close()
			return
		}
	}

	// From ws -> dap
	go func() {
		defer wsConn.Close()
		defer toDap.Close()
		for {
			_, message, err := wsConn.ReadMessage()
			if err != nil {
				log.Println("ws -> dap error reading:", err)
				break
			}
			log.Printf("ws -> dap: %s\n", message)

			_, err = toDap.Write([]byte(fmt.Sprintf("Content-Length: %d\r\n\r\n", len(message))))
			if err != nil {
				log.Println("ws -> dap error writing header:", err)
				break
			}
			_, err = toDap.Write(message)
			if err != nil {
				log.Println("ws -> dap error writing message:", err)
				break
			}
		}
	}()

	// From dap -> ws
	go func() {
		defer wsConn.Close()
		defer fromDap.Close()
		// reader := bufio.NewReader(dapConn)
		// reader := bufio.NewReader(conn)
		contentLength := -1
		var debugBuilder strings.Builder
		var headerBuilder strings.Builder
		newLine := false
		for {
			b := make([]byte, 1)

			len, err := fromDap.Read(b)
			if err != nil || len != 1 {
				log.Println("dap -> ws error reading header byte, read so far '"+debugBuilder.String()+"':", err)
				break
			}

			debugBuilder.Write(b)
			if b[0] == '\n' {
				if newLine {
					// Two consecutive newlines have been read, which signals the start of the message content
					if contentLength < 0 {
						log.Println("dap -> ws error missing Content-Length, read so far '"+debugBuilder.String()+"':", err)
					} else {
						message := make([]byte, contentLength)
						len, err := fromDap.Read(message)
						if len != contentLength || err != nil {
							log.Printf("dap -> ws error missing data, expected %d bytes, got %d bytes\n", contentLength, len)
						} else if len != contentLength || err != nil {
							log.Println("dap -> ws error reading data", err)
						} else {
							log.Printf("dap -> ws: %s\n", message)
							wsConn.WriteMessage(websocket.TextMessage, message)
						}
					}
					contentLength = -1
					debugBuilder.Reset()
				} else if headerBuilder.Len() > 0 {
					header := headerBuilder.String()
					lenStr := strings.TrimPrefix(header, "Content-Length: ")
					if lenStr != header {
						contentLength, err = strconv.Atoi(lenStr)
						if err != nil {
							contentLength = -1
							log.Println("dap -> ws error converting Content-Length's value", err)
						}
					}
					headerBuilder.Reset()
				}
				newLine = true
			} else if b[0] != '\r' {
				// Add the input to the current header line
				headerBuilder.Write(b)
				newLine = false
			}
		}
	}()
}

func home(w http.ResponseWriter, r *http.Request) {
	if r.URL.String() == "/" {
		log.Printf("Serving Home (" + r.URL.String() + ")\n")
		homeTemplate.Execute(w, "ws://"+r.Host+"/services/debug-adapter/session-id")
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
