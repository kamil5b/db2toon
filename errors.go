package db2toon

import "errors"

var (
	errDialectRequired  = errors.New("dialect is required")
	errExactlyOneSource = errors.New("exactly one of db and dump is required")
	errNegativeSample   = errors.New("example_sample must not be negative")
)

// Error describes an extraction failure without including a DSN, password,
// dump path, or dump contents in its rendered message.
type Error struct {
	Operation string
	Dialect   string
	Source    string
	Line      int
	Statement int
	Err       error
}

func (e *Error) Error() string {
	message := "db2toon: " + e.Operation
	if e.Dialect != "" {
		message += " (" + e.Dialect + ")"
	}
	if e.Source != "" {
		message += " [" + e.Source + "]"
	}
	if e.Line > 0 {
		message += " at line " + formatInt(e.Line)
	}
	if e.Err != nil {
		switch e.Err {
		case errDialectRequired:
			return message + ": dialect is required"
		case errExactlyOneSource:
			return message + ": exactly one of db and dump is required"
		case errNegativeSample:
			return message + ": example_sample must not be negative"
		}
	}
	return message + ": extraction failed"
}

func (e *Error) Unwrap() error { return e.Err }
func formatInt(n int) string {
	if n == 0 {
		return "0"
	}
	var b [24]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
