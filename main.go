package main

import (
	"fmt"
	"os"

	"github.com/spotlightpa/sheets-uploader/sheets"
)

func main() {
	c := sheets.FromArgs(os.Args[1:])
	if err := c.Exec(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
