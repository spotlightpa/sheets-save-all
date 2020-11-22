# sheets-uploader
Copy a Google Sheets document to the cloud storage as a CSV. Requires an [OAuth 2.0 token](https://support.google.com/googleapi/answer/6158849) to access Google Sheets.


## Installation

First install [Go](http://golang.org).

If you just want to install the binary to your current directory and don't care about the source code, run

```shell
GOBIN="$(pwd)" GOPATH="$(mktemp -d)" go get github.com/spotlightpa/sheets-uploader
```
