// Copyright 2023 CodeMaker AI Inc. All rights reserved.

package client

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"github.com/codemakerai/codemaker-sdk-go/cert"
	"github.com/codemakerai/codemaker-sdk-go/stub"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"io"
)

const (
	endpoint = "process.codemaker.ai:443"

	defaultEnableCompression             = true
	defaultMinimumCompressionPayloadSize = 2048

	authorizationHeader = "Authorization"
	bearerToken         = "Bearer %s"

	defaultMaxRetries = 5
)

type Client interface {
	Process(ctx context.Context, request *ProcessRequest) (*ProcessResponse, error)
}

type defaultClient struct {
	Client
	config Config
	client stub.CodemakerServiceClient
}

func loadTls() (*tls.Config, error) {
	certPool := x509.NewCertPool()
	if !certPool.AppendCertsFromPEM(cert.CA()) {
		return nil, fmt.Errorf("failed to add server CA's certificate")
	}

	config := &tls.Config{
		RootCAs: certPool,
	}
	return config, nil
}

func NewClient(config Config) (Client, error) {
	e := endpoint
	if config.Endpoint != nil {
		e = *config.Endpoint
	}

	tls, err := loadTls()
	if err != nil {
		return nil, err
	}

	conn, err := grpc.NewClient(e, grpc.WithTransportCredentials(credentials.NewTLS(tls)))
	if err != nil {
		return nil, err
	}

	c := stub.NewCodemakerServiceClient(conn)

	client := &defaultClient{
		config: config,
		client: c,
	}
	return client, nil
}

func (c *defaultClient) Process(ctx context.Context, request *ProcessRequest) (*ProcessResponse, error) {
	req, err := c.createProcessRequest(request)
	if err != nil {
		return nil, err
	}

	resp, err := c.doProcess(ctx, req)
	if err != nil {
		return nil, err
	}

	return c.createProcessResponse(resp)
}

func (c *defaultClient) createProcessRequest(request *ProcessRequest) (*stub.ProcessRequest, error) {
	input, err := c.createInput(request.Input)
	if err != nil {
		return nil, err
	}

	req := &stub.ProcessRequest{
		Mode:     c.mapMode(request.Mode),
		Language: c.mapLanguage(request.Language),
		Input:    input,
		Options:  c.createProcessOptions(request.Options),
	}
	return req, nil
}

func (c *defaultClient) doProcess(ctx context.Context, req *stub.ProcessRequest) (*stub.ProcessResponse, error) {
	maxRetries := c.maxRetries()
	var lastError error
	for retry := 0; retry < maxRetries; retry++ {
		resp, err := c.client.Process(c.createMetadata(ctx), req)
		if err == nil {
			return resp, err
		} else if status.Code(err) == codes.DeadlineExceeded {
			lastError = err
		} else {
			return resp, err
		}
	}

	return nil, fmt.Errorf("error invoking CodeMaker AI API %v", lastError)
}

func (c *defaultClient) createProcessResponse(resp *stub.ProcessResponse) (*ProcessResponse, error) {
	source, err := c.decodeOutput(resp.Output.Source)
	if err != nil {
		return nil, err
	}

	response := &ProcessResponse{
		Source: source,
	}
	return response, nil
}

func (c *defaultClient) createMetadata(ctx context.Context) context.Context {
	md := metadata.Pairs(authorizationHeader, fmt.Sprintf(bearerToken, c.config.ApiKey))
	return metadata.NewOutgoingContext(ctx, md)
}

func (c *defaultClient) createInput(input Input) (*stub.Input, error) {
	encodedInput, err := c.encodeInput(input.Source)
	if err != nil {
		return nil, err
	}

	result := &stub.Input{
		Source: encodedInput,
	}
	return result, nil
}

func (c *defaultClient) encodeInput(source string) (*stub.Source, error) {
	encoding := stub.Encoding_NONE
	input := []byte(source)
	checksum := c.checksum(input)

	if c.enableCompression() && len(input) >= c.minimumCompressionPayloadSize() {
		encoding = stub.Encoding_GZIP
		data, err := c.compress(input)
		if err != nil {
			return nil, err
		}
		input = data
	}

	s := &stub.Source{
		Content:  input,
		Encoding: encoding,
		Checksum: checksum,
	}
	return s, nil
}

func (c *defaultClient) decodeOutput(source *stub.Source) (string, error) {
	content := source.Content

	if source.Encoding == stub.Encoding_GZIP {
		data, err := c.decompress(content)
		if err != nil {
			return "", err
		}
		content = data
	}

	return string(content), nil
}

func (c *defaultClient) createProcessOptions(options *Options) *stub.ProcessOptions {
	modify := stub.Modify_UNMODIFIED
	if options.Modify != nil {
		modify = c.mapModify(*options.Modify)
	}

	codePath := ""
	if options.CodePath != nil {
		codePath = *options.CodePath
	}

	model := ""
	if options.Model != nil {
		model = *options.Model
	}

	return &stub.ProcessOptions{
		Modify:   modify,
		CodePath: codePath,
		Model:    model,
	}
}

func (c *defaultClient) compress(output []byte) ([]byte, error) {
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)

	_, err := writer.Write(output)
	if err != nil {
		return nil, err
	}

	if err := writer.Flush(); err != nil {
		return nil, err
	}

	if err := writer.Close(); err != nil {
		return nil, err
	}

	return buffer.Bytes(), nil
}

func (c *defaultClient) decompress(output []byte) ([]byte, error) {
	data := bytes.NewReader(output)
	reader, err := gzip.NewReader(data)
	if err != nil {
		return nil, err
	}

	output, err = io.ReadAll(reader)
	if err != nil {
		return nil, err
	}

	return output, nil
}

func (c *defaultClient) checksum(source []byte) string {
	sum := sha256.Sum256(source)
	return fmt.Sprintf("%x", sum)
}

func (c *defaultClient) enableCompression() bool {
	if c.config.EnableCompression != nil {
		return *c.config.EnableCompression
	}
	return defaultEnableCompression
}

func (c *defaultClient) minimumCompressionPayloadSize() int {
	if c.config.MinimumCompressionPayloadSize != nil {
		return *c.config.MinimumCompressionPayloadSize
	}
	return defaultMinimumCompressionPayloadSize
}

func (c *defaultClient) maxRetries() int {
	if c.config.MaxRetries != nil {
		return *c.config.MaxRetries
	}
	return defaultMaxRetries
}

func (c *defaultClient) mapMode(mode string) stub.Mode {
	switch mode {
	case ModeCode:
		return stub.Mode_CODE
	case ModeDocument:
		return stub.Mode_DOCUMENT
	case ModeFixSyntax:
		return stub.Mode_FIX_SYNTAX
	}
	panic(fmt.Sprintf("Unsupported mode %s", mode))
}

func (c *defaultClient) mapLanguage(language string) stub.Language {
	switch language {
	case LanguageC:
		return stub.Language_C
	case LanguageCPP:
		return stub.Language_CPP
	case LanguageJavaScript:
		return stub.Language_JAVASCRIPT
	case LanguagePHP:
		return stub.Language_PHP
	case LanguageJava:
		return stub.Language_JAVA
	case LanguageCSharp:
		return stub.Language_CSHARP
	case LanguageGo:
		return stub.Language_GO
	case LanguageKotlin:
		return stub.Language_KOTLIN
	case LanguageTypeScript:
		return stub.Language_TYPESCRIPT
	case LanguageRust:
		return stub.Language_RUST
	}
	panic(fmt.Sprintf("Unsupported language %s", language))
}

func (c *defaultClient) mapModify(modify string) stub.Modify {
	switch modify {
	case ModifyNone:
		return stub.Modify_UNMODIFIED
	case ModifyReplace:
		return stub.Modify_REPLACE
	}
	panic(fmt.Sprintf("Unsupported modify %s", modify))
}
