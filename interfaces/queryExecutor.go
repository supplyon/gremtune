package interfaces

import (
	"encoding/json"
	"fmt"
)

type QueryExecutor interface {
	Close() error
	IsConnected() bool
	LastError() error
	Execute(query string) (resp []Response, err error)
	ExecuteAsync(query string, responseChannel chan AsyncResponse) (err error)
	ExecuteFileWithBindings(path string, bindings, rebindings map[string]string) (resp []Response, err error)
	ExecuteFile(path string) (resp []Response, err error)
	ExecuteWithBindings(query string, bindings, rebindings map[string]string) (resp []Response, err error)
	Ping() error
}

const (
	StatusSuccess                  = 200
	StatusNoContent                = 204
	StatusPartialContent           = 206
	StatusUnauthorized             = 401
	StatusAuthenticate             = 407
	StatusMalformedRequest         = 498
	StatusInvalidRequestArguments  = 499
	StatusServerError              = 500
	StatusScriptEvaluationError    = 597
	StatusServerTimeout            = 598
	StatusServerSerializationError = 599
)

// Response structs holds the entire response from requests to the gremlin server
type Response struct {
	RequestID string `json:"requestId"`
	Status    Status `json:"status"`
	Result    Result `json:"result"`
}

// Status struct is used to hold properties returned from requests to the gremlin server
type Status struct {
	Message    string                 `json:"message"`
	Code       int                    `json:"code"`
	Attributes map[string]interface{} `json:"attributes"`
}

// Result struct is used to hold properties returned for results from requests to the gremlin server
type Result struct {
	// Query Response Data
	Data json.RawMessage        `json:"data"`
	Meta map[string]interface{} `json:"meta"`
}

// AsyncResponse structs holds the entire response from requests to the gremlin server
type AsyncResponse struct {
	Response     Response `json:"response"`     //Partial Response object
	ErrorMessage string   `json:"errorMessage"` // Error message if there was an error
}

// String returns a string representation of the Response struct
func (r Response) String() string {
	return fmt.Sprintf("Response \nRequestID: %v, \nStatus: {%#v}, \nResult: {%#v}\n", r.RequestID, r.Status, r.Result)
}
