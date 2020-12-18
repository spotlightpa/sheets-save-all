# sheets-uploader
Copy a Google Sheets document to the cloud storage as a CSV. Requires an [OAuth 2.0 token](https://support.google.com/googleapi/answer/6158849) to access Google Sheets.


## Installation

First install [Go](http://golang.org).

If you just want to install the binary to your current directory and don't care about the source code, run

```shell
GOBIN="$(pwd)" GOPATH="$(mktemp -d)" go get github.com/spotlightpa/sheets-uploader
```

## Screenshots

```bash
$ sheets-uploader -h

sheets-uploader is a tool to save all sheets in Google Sheets document to cloud storage.

-path and -filename are Go templates and can use any property of the document
or sheet object respectively. See gopkg.in/Iwark/spreadsheet.v2 for properties.

If -google-client-secret is not specified, the default Google credentials will be used:

1. A JSON file whose path is specified by the GOOGLE_APPLICATION_CREDENTIALS
   environment variable.
2. A JSON file in a location known to the gcloud command-line tool.On Windows,
   this is %APPDATA%/gcloud/application_default_credentials.json. On other
   systems, $HOME/.config/gcloud/application_default_credentials.json.

If -google-client-secret is specified, it must be a base64 encoded version of
application_default_credentials.json because the '\n' in the JSON is often
mangled by the environment.

When connecting to AWS S3, the AWS default credentials are used:

1. Environment Credentials - AWS_ACCESS_KEY_ID or AWS_ACCESS_KEY and
   AWS_SECRET_ACCESS_KEY or AWS_SECRET_KEY.
2. Shared Credentials file (~/.aws/credentials)
3. EC2 Instance Role Credentials

Bucket URL can set S3 bucket and region like s3://bucket-name?region=us-east-1.
The special bucket URLs 'mem://' (for dry run testing) and 'file://.' for local
storage can also be used.

Options for sheets-uploader:

  -bucket-url URL
        URL for destination bucket (default "file://.")
  -cache-control value
        value for Cache-Control header (default "max-age=900,public")
  -crlf
        use Windows-style line endings
  -dist distibution ID
        distibution ID for AWS CloudFront CDN invalidation
  -filename string
        file name for files (default "{{.Properties.Index}} {{.Properties.Title}}.csv")
  -google-client-secret base64 encoded JSON
        base64 encoded JSON of Google client secret
  -path string
        path to save files in (default "{{.Properties.Title}}")
  -quiet
        don't log activity
  -sheet value
        Google Sheet ID
  -workers int
        number of upload workers (default 10)

Options can also be passed as environment variables prepended with
SHEETS_UPLOADER, e.g. SHEETS_UPLOADER_BUCKET_URL.
```

## Features

- Writes to AWS S3 (`-bucket-url s3://mybucket?region=us-east-1`) or local filesystem

- Customize file names by template

- Set remote cache control headers

- Concurrent uploading

- Automatically skips files that have been uploaded

- Automatic CloudFront CDN invalidation

- Flags can be passed directly (`-sheet`, `--sheet`) or as environment variables `SHEETS_UPLOADER_SHEET=... sheets-uploader`
