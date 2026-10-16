//go:build darwin && cgo

// Command baltimore-app is the native macOS application: it starts the
// dashboard server on a private loopback port and shows it in a Cocoa window
// (WKWebView) — no browser, no terminal. Build the double-clickable
// "Baltimore Engine.app" bundle with `./run.sh app`.
package main

/*
#cgo CFLAGS: -x objective-c -fobjc-arc -mmacosx-version-min=13.0
#cgo LDFLAGS: -framework Cocoa -framework WebKit -mmacosx-version-min=13.0
#include <stdlib.h>
#include "window_darwin.h"
*/
import "C"

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"unsafe"

	"github.com/aidan/baltimore-engine/internal/ui"
)

// Cocoa requires the event loop on the process's main thread. Locking the
// main goroutine to its OS thread during init guarantees main() runs there.
func init() { runtime.LockOSThread() }

// bundledData finds the dataset snapshot: inside the .app bundle
// (Contents/Resources/data) when packaged, or ./data during development.
func bundledData() string {
	if exe, err := os.Executable(); err == nil {
		if exe, err = filepath.EvalSymlinks(exe); err == nil {
			d := filepath.Join(filepath.Dir(exe), "..", "Resources", "data")
			if _, err := os.Stat(d); err == nil {
				return filepath.Clean(d)
			}
		}
	}
	return "data"
}

func main() {
	srv := ui.New(ui.Config{BundledData: bundledData(), UserData: ui.DefaultUserData()})
	url, err := srv.Start()
	if err != nil {
		fmt.Fprintln(os.Stderr, "could not start dashboard:", err)
		os.Exit(1)
	}
	cURL, cTitle := C.CString(url), C.CString("Baltimore Engine")
	defer C.free(unsafe.Pointer(cURL))
	defer C.free(unsafe.Pointer(cTitle))
	C.RunApp(cURL, cTitle) // blocks until the user quits
}
