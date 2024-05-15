// Copyright 2023 CodeMaker AI Inc. All rights reserved.

package client

type Config struct {
	ApiKey                        string
	Endpoint                      *string
	EnableCompression             *bool
	MinimumCompressionPayloadSize *int
	MaxRetries                    *int
}
