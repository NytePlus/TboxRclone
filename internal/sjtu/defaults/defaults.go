// Package defaults is the single source of product runtime defaults and shared
// protocol/security ceilings. Only values exposed by backend Options may be
// overridden by user configuration.
package defaults

import "time"

const (
	Endpoint                       = "https://pan.sjtu.edu.cn"
	OrganizationID                 = "1"
	MaxUploadBytes           int64 = 2 << 30
	MaxSpoolBytes            int64 = 4 << 30
	MultipartPartSize        int64 = 4 << 20
	MultipartWorkers               = 4
	MultipartBatchSize             = 50
	MaxActiveMutations             = 4
	ReadRetries                    = 2
	TransferRetries                = 3
	MinMultipartPartSize     int64 = 1 << 20
	MaxMultipartPartSize     int64 = 64 << 20
	MaxMultipartParts              = 10000
	MaxMultipartBatchSize          = 50
	MaxMultipartWorkers            = 32
	MaxConfiguredRetries           = 10
	MaxConfiguredMutations         = 64
	MaxJSONResponseBytes     int64 = 4 << 20
	MaxListPages                   = 100000
	MaxConfiguredUploadBytes int64 = 1 << 40
	MaxConfiguredSpoolBytes  int64 = 1 << 50
)

const (
	RequestTimeout     = 2 * time.Minute
	ReadRetryDelay     = 200 * time.Millisecond
	TransferRetryDelay = time.Second
)
