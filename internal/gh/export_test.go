package gh

import "github.com/claytoncasey01/conn/internal/run"

// ErrorOf is errorOf, for the tests.
func ErrorOf(result run.Result) *Error { return errorOf(result) }
