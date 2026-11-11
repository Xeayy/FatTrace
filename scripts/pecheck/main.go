// Command pecheck prints the PE subsystem of each given executable.
// 2 = Windows GUI (no console), 3 = Windows console. Used to verify that
// fatgui is built as a GUI app while fat stays a console app.
package main

import (
	"debug/pe"
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: pecheck <exe>...")
		os.Exit(2)
	}
	bad := false
	for _, p := range os.Args[1:] {
		f, err := pe.Open(p)
		if err != nil {
			fmt.Printf("%s: error: %v\n", p, err)
			bad = true
			continue
		}
		var sub uint16
		switch oh := f.OptionalHeader.(type) {
		case *pe.OptionalHeader64:
			sub = oh.Subsystem
		case *pe.OptionalHeader32:
			sub = oh.Subsystem
		}
		f.Close()
		fmt.Printf("%s: subsystem=%d\n", p, sub)
	}
	if bad {
		os.Exit(1)
	}
}
