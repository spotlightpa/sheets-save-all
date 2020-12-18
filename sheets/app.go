package sheets

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/csv"
	"flag"
	"fmt"
	"hash"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/cloudfront"
	"github.com/carlmjohnson/flagext"
	"github.com/henvic/ctxsignal"
	"gocloud.dev/blob"
	_ "gocloud.dev/blob/fileblob"
	_ "gocloud.dev/blob/memblob"
	"golang.org/x/oauth2/google"
	spreadsheet "gopkg.in/Iwark/spreadsheet.v2"
)

const AppName = "sheets-uploader"

func CLI(args []string) error {
	var conf Config
	if err := conf.FromArgs(args); err != nil {
		return err
	}
	if err := conf.Exec(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %+v\n", err)
		return err
	}
	return nil
}

func (conf *Config) FromArgs(args []string) error {
	fl := flag.NewFlagSet(AppName, flag.ExitOnError)
	fl.IntVar(&conf.NWorkers, "workers", 10, "number of upload workers")
	fl.StringVar(&conf.SheetID, "sheet", "", "Google Sheet ID")
	flagext.Callback(fl, "google-client-secret", "", "`base64 encoded JSON` of Google client secret",
		func(s string) error {
			var err error
			conf.GoogleClientSecret, err = base64.StdEncoding.DecodeString(s)
			return err
		})
	fl.StringVar(&conf.PathTemplate, "path", "{{.Properties.Title}}", "path to save files in")
	fl.StringVar(&conf.FileTemplate, "filename", "{{.Properties.Index}} {{.Properties.Title}}.csv",
		"file name for files")
	fl.StringVar(&conf.BucketURL, "bucket-url", "file://.",
		"`URL` for destination bucket")
	fl.StringVar(&conf.CloudFrontDist, "dist", "",
		"`distibution ID` for AWS CloudFront CDN invalidation")

	fl.StringVar(&conf.CacheControl, "cache-control", "max-age=900,public",
		"`value` for Cache-Control header")
	fl.BoolVar(&conf.UseCRLF, "crlf", false, "use Windows-style line endings")

	conf.Logger = log.New(os.Stderr, AppName+" ", log.LstdFlags)
	flagext.LoggerVar(fl, conf.Logger, "quiet", flagext.LogSilent,
		"don't log activity")
	fl.Usage = func() {
		fmt.Fprintf(os.Stderr,
			`sheets-uploader is a tool to save all sheets in Google Sheets document to cloud storage.

-path and -filename are Go templates and can use any property of the document
or sheet object respectively. See gopkg.in/Iwark/spreadsheet.v2 for properties.

If -google-client-secret is not specified, the default Google credentials will be used:

1. A JSON file whose path is specified by the GOOGLE_APPLICATION_CREDENTIALS
   environment variable.
2. A JSON file in a location known to the gcloud command-line tool.On Windows,
   this is %%APPDATA%%/gcloud/application_default_credentials.json. On other
   systems, $HOME/.config/gcloud/application_default_credentials.json.

If -google-client-secret is specified, it must be a base64 encoded version of
application_default_credentials.json because the '\n' in the JSON is often
mangled by the environment.

When connecting to AWS S3, the AWS default credentials are used:

1. Environment Credentials - AWS_ACCESS_KEY_ID or AWS_ACCESS_KEY and
   AWS_SECRET_ACCESS_KEY or AWS_SECRET_KEY.
2. Shared Credentials file (~/.aws/credentials)
3. EC2 Instance Role Credentials

Usage of sheets-uploader:

`,
		)
		fl.PrintDefaults()
	}
	if err := fl.Parse(args); err != nil {
		return err
	}

	if err := flagext.ParseEnv(fl, AppName); err != nil {
		return err
	}

	if err := flagext.MustHave(fl, "sheet"); err != nil {
		return err
	}

	return nil
}

type Config struct {
	NWorkers           int
	SheetID            string
	GoogleClientSecret []byte
	PathTemplate       string
	FileTemplate       string
	BucketURL          string
	CacheControl       string
	UseCRLF            bool
	CloudFrontDist     string
	Logger             *log.Logger
}

func (c *Config) Exec() (err error) {
	if c.NWorkers < 1 {
		return fmt.Errorf("invalid number of workers: %d", c.NWorkers)
	}

	pt, err := template.New("path").Parse(c.PathTemplate)
	if err != nil {
		return fmt.Errorf("path template problem: %v", err)
	}
	ft, err := template.New("file").Parse(c.FileTemplate)
	if err != nil {
		return fmt.Errorf("file path template problem: %v", err)
	}

	ctx, cancel := ctxsignal.WithTermination(context.Background())
	defer cancel()

	c.Logger.Printf("opening cloud storage %q", c.BucketURL)
	b, err := blob.OpenBucket(ctx, c.BucketURL)
	if err != nil {
		return fmt.Errorf("could not open bucket: %v", err)
	}
	defer deferClose(&err, b.Close)

	client, err := c.googleClient(ctx)
	if err != nil {
		return err
	}

	c.Logger.Printf("connecting to Google Sheets for %q", c.SheetID)
	service := spreadsheet.NewServiceWithClient(client)
	doc, err := service.FetchSpreadsheet(c.SheetID)
	if err != nil {
		return fmt.Errorf("failure getting Google Sheet: %v", err)
	}

	c.Logger.Printf("got %q", doc.Properties.Title)

	var dirBuf strings.Builder
	if err = pt.Execute(&dirBuf, doc); err != nil {
		return fmt.Errorf("could not use path template: %v", err)
	}
	dir := dirBuf.String()

	c.Logger.Printf("%d upload workers", c.NWorkers)
	type result struct {
		path string
		err  error
	}
	var (
		sheetCh   = make(chan *spreadsheet.Sheet)
		resultCh  = make(chan result)
		waitingOn = 0
		opts      = blob.WriterOptions{
			CacheControl: c.CacheControl,
			ContentType:  "text/csv",
		}
		paths []string
	)
	for i := 0; i < c.NWorkers; i++ {
		go func() {
			var (
				sb  strings.Builder
				buf bytes.Buffer
				h   = md5.New()
			)
			for {
				select {
				case s, ok := <-sheetCh:
					if !ok {
						return
					}
					fullpath, err := c.uploadSheet(
						ctx, b, &sb, &buf, ft, s, dir, h, &opts)
					resultCh <- result{fullpath, err}
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	for len(doc.Sheets) > 0 || waitingOn > 0 {
		workCh := sheetCh
		var sheet *spreadsheet.Sheet
		if len(doc.Sheets) > 0 {
			sheet = &doc.Sheets[0]
		} else {
			workCh = nil
		}
		select {
		case workCh <- sheet:
			waitingOn++
			doc.Sheets = doc.Sheets[1:]
		case res := <-resultCh:
			waitingOn--
			if res.err != nil {
				return res.err
			}
			if res.path != "" {
				paths = append(paths, res.path)
			}
		}
	}
	if len(paths) > 0 && c.CloudFrontDist != "" {
		_, err = c.invalidate(paths)
		return err
	}
	return nil
}

func (c *Config) googleClient(ctx context.Context) (*http.Client, error) {
	if len(c.GoogleClientSecret) > 0 {
		c.Logger.Printf("using base64 Google credentials")
		conf, err := google.JWTConfigFromJSON(c.GoogleClientSecret, spreadsheet.Scope)
		if err != nil {
			return nil, fmt.Errorf("could not parse Google credentials: %v", err)
		}
		return conf.Client(ctx), nil
	}
	c.Logger.Printf("using default Google credentials")
	client, err := google.DefaultClient(ctx, spreadsheet.Scope)
	if err != nil {
		return nil, fmt.Errorf("could not find Google credentials: %v", err)
	}
	return client, nil
}

func (c *Config) uploadSheet(ctx context.Context, b *blob.Bucket, sb *strings.Builder, buf *bytes.Buffer, ft *template.Template, s *spreadsheet.Sheet, dir string, h hash.Hash, opts *blob.WriterOptions) (fullpath string, err error) {
	sb.Reset()
	if err = ft.Execute(sb, s); err != nil {
		return "", fmt.Errorf("could not use file path template: %v", err)
	}
	file := sb.String()

	if err = c.makeCSV(buf, s.Rows); err != nil {
		return "", err
	}

	fullpath = path.Join(dir, file)
	var returnPath string
	c.Logger.Printf("checking existing %q in %q", fullpath, c.BucketURL)
	attrs, err := b.Attributes(ctx, fullpath)
	if err == nil && attrs.MD5 != nil {
		// Get checksum
		h.Reset()
		if _, err := h.Write(buf.Bytes()); err != nil {
			return "", err
		}
		if string(h.Sum(nil)) == string(attrs.MD5) {
			c.Logger.Printf("skipping %q; already uploaded", fullpath)
			return "", nil
		}
		returnPath = fullpath
	}

	c.Logger.Printf("writing %q to %q", fullpath, c.BucketURL)
	if err = b.WriteAll(ctx, fullpath, buf.Bytes(), opts); err != nil {
		return "", err
	}
	return returnPath, nil
}

func (c *Config) makeCSV(buf *bytes.Buffer, rows [][]spreadsheet.Cell) (err error) {
	buf.Reset()
	w := csv.NewWriter(buf)
	w.UseCRLF = c.UseCRLF
	defer w.Flush()
	defer deferClose(&err, w.Error)

	for _, row := range rows {
		record := make([]string, 0, len(row))
		for _, cell := range row {
			record = append(record, cell.Value)
		}
		if blank(record) {
			continue
		}
		err = w.Write(record)
		if err != nil {
			return err
		}
	}
	return nil
}

func blank(record []string) bool {
	for _, s := range record {
		if s != "" {
			return false
		}
	}
	return true
}

func deferClose(err *error, f func() error) {
	newErr := f()
	if *err == nil && newErr != nil {
		*err = fmt.Errorf("problem closing: %v", newErr)
	}
}

func makepaths(paths []string) *cloudfront.Paths {
	items := make([]*string, len(paths))
	for i := range paths {
		items[i] = &paths[i]
	}
	quantity := int64(len(items))
	return &cloudfront.Paths{
		Items:    items,
		Quantity: &quantity,
	}
}

func (c *Config) invalidate(paths []string) (id string, err error) {
	for i, path := range paths {
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		path = url.PathEscape(path)
		path = strings.ReplaceAll(path, "%2F", "/")
		paths[i] = path
	}
	c.Logger.Printf("invalidating %v in CloudFront %s", paths, c.CloudFrontDist)

	cf := cloudfront.New(session.Must(session.NewSession()))
	callerReference := time.Now().Format("20060102150405")

	result, err := cf.CreateInvalidation(&cloudfront.CreateInvalidationInput{
		DistributionId: &c.CloudFrontDist,
		InvalidationBatch: &cloudfront.InvalidationBatch{
			CallerReference: &callerReference,
			Paths:           makepaths(paths),
		},
	})
	if err != nil {
		return "", err
	}

	return *result.Invalidation.Id, nil
}
