package main

import (
	"os"

	"github.com/carlmjohnson/exitcode"
	"github.com/spotlightpa/sheets-uploader/sheets"
)

func main() {
	exitcode.Exit(sheets.CLI(os.Args[1:]))
}
