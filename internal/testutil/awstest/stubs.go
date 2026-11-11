package awstest

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"slices"
	"strings"
)

// Stub is a canned response. Status 0 means 200.
type Stub struct {
	Status int
	Body   string
}

const serviceDynamoDB = "dynamodb"

const s3XMLNS = "http://s3.amazonaws.com/doc/2006-03-01/"

// bucketSubresources lists the bucket configuration queries the fake answers, with the answer
// for a bucket that has none: AWS encrypts with SSE-S3 by default since 2023.
func bucketSubresources() map[string]Stub {
	missing := func(code, msg string) Stub {
		return Stub{Status: http.StatusNotFound, Body: s3ErrorXML(code, msg)}
	}
	return map[string]Stub{
		"versioning": {Body: `<VersioningConfiguration xmlns="` + s3XMLNS + `"/>`},
		"encryption": {Body: `<ServerSideEncryptionConfiguration xmlns="` + s3XMLNS + `"><Rule>` +
			`<ApplyServerSideEncryptionByDefault><SSEAlgorithm>AES256</SSEAlgorithm></ApplyServerSideEncryptionByDefault>` +
			`<BucketKeyEnabled>false</BucketKeyEnabled></Rule></ServerSideEncryptionConfiguration>`},
		subPolicy: missing("NoSuchBucketPolicy", "The bucket policy does not exist"),
		"publicAccessBlock": missing("NoSuchPublicAccessBlockConfiguration",
			"The public access block configuration was not found"),
		"ownershipControls": missing("OwnershipControlsNotFoundError", "The bucket ownership controls were not found"),
		"replication": missing(
			"ReplicationConfigurationNotFoundError",
			"The replication configuration was not found",
		),
		"lifecycle": missing("NoSuchLifecycleConfiguration", "The lifecycle configuration does not exist"),
		"logging":   {Body: `<BucketLoggingStatus xmlns="` + s3XMLNS + `"/>`},
		"location":  {},
	}
}

// subPolicy is the query key of a bucket policy request.
const subPolicy = "policy"

func s3ErrorXML(code, msg string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>` + "\n" +
		`<Error><Code>` + code + `</Code><Message>` + msg + `</Message><RequestId>0000000000000000</RequestId></Error>`
}

// subresource returns the bucket configuration a GET asks for, or "".
func subresource(req *http.Request) string {
	if req.Method != http.MethodGet {
		return ""
	}
	q := req.URL.Query()
	for name := range bucketSubresources() {
		if _, ok := q[name]; ok {
			return name
		}
	}
	return ""
}

func bucketOf(host string) string {
	bucket, _, _ := strings.Cut(host, ".")
	return bucket
}

func (f *Fake) region(bucket string) string {
	if r := f.Regions[bucket]; r != "" {
		return r
	}
	return "us-east-1"
}

func (f *Fake) bucketConfig(w http.ResponseWriter, req *http.Request) {
	bucket, sub := bucketOf(req.URL.Host), subresource(req)
	if ok, _ := f.backend.BucketExists(bucket); !ok && f.Regions[bucket] == "" {
		writeS3Error(w, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist")
		return
	}
	if sub == "location" {
		loc := f.region(bucket)
		if loc == "us-east-1" {
			loc = ""
		}
		writeStub(
			w,
			"application/xml",
			Stub{Body: `<LocationConstraint xmlns="` + s3XMLNS + `">` + loc + `</LocationConstraint>`},
		)
		return
	}
	stub, ok := f.Stubs["s3:"+bucket+"?"+sub]
	if !ok {
		stub = bucketSubresources()[sub]
	}
	contentType := "application/xml"
	if sub == "policy" && stub.Status == 0 {
		contentType = "application/json"
	}
	writeStub(w, contentType, stub)
}

func writeStub(w http.ResponseWriter, contentType string, s Stub) {
	w.Header().Set("Content-Type", contentType)
	if s.Status != 0 {
		w.WriteHeader(s.Status)
	}
	_, _ = io.WriteString(w, s.Body)
}

// listBuckets lists the gofakes3 buckets with their Regions.
func (f *Fake) listBuckets(w http.ResponseWriter) {
	buckets, err := f.backend.ListBuckets()
	if err != nil {
		writeS3Error(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	var b strings.Builder
	b.WriteString(`<ListAllMyBucketsResult xmlns="` + s3XMLNS + `"><Owner><ID>fake-owner</ID></Owner><Buckets>`)
	names := make([]string, len(buckets))
	for i, bk := range buckets {
		names[i] = bk.Name
	}
	slices.Sort(names)
	for _, n := range names {
		fmt.Fprintf(&b, `<Bucket><Name>%s</Name><CreationDate>2024-01-01T00:00:00.000Z</CreationDate>`+
			`<BucketRegion>%s</BucketRegion></Bucket>`, html.EscapeString(n), f.region(n))
	}
	b.WriteString(`</Buckets></ListAllMyBucketsResult>`)
	writeStub(w, "application/xml", Stub{Body: b.String()})
}

func (f *Fake) s3control(w http.ResponseWriter, req *http.Request) {
	account, _, _ := strings.Cut(req.URL.Host, ".")
	stub, ok := f.Stubs["s3control:"+account+"?publicAccessBlock"]
	if !ok {
		stub = Stub{Status: http.StatusNotFound, Body: s3ErrorXML("NoSuchPublicAccessBlockConfiguration",
			"The public access block configuration was not found")}
	}
	writeStub(w, "application/xml", stub)
}

// jsonService names the JSON-protocol service a host belongs to, or "".
func jsonService(host string) string {
	for _, s := range []string{"kms", serviceDynamoDB, "cloudtrail"} {
		if strings.HasPrefix(host, s+".") {
			return s
		}
	}
	return ""
}

func (f *Fake) json(w http.ResponseWriter, req *http.Request, service string) {
	target := req.Header.Get("X-Amz-Target")
	op := target[strings.LastIndex(target, ".")+1:]
	body, _ := io.ReadAll(req.Body)
	var in map[string]any
	_ = json.Unmarshal(body, &in)
	resource := ""
	for _, k := range []string{"KeyId", "TableName", "TrailName"} {
		if v, ok := in[k].(string); ok {
			resource = v
			break
		}
	}
	contentType := "application/x-amz-json-1.1"
	if service == serviceDynamoDB {
		contentType = "application/x-amz-json-1.0"
	}
	stub, ok := f.Stubs[service+":"+op+":"+resource]
	if !ok {
		stub, ok = f.Stubs[service+":"+op]
	}
	if !ok {
		code := map[string]string{
			"kms": "NotFoundException", serviceDynamoDB: "ResourceNotFoundException", "cloudtrail": "TrailNotFoundException",
		}[service]
		stub = Stub{
			Status: http.StatusBadRequest,
			Body:   fmt.Sprintf(`{"__type":"%s","message":"%s not found"}`, code, resource),
		}
	}
	writeStub(w, contentType, stub)
}
