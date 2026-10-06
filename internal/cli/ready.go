package cli

import (
	"errors"
	"strings"
)

// The daemon tells the launcher how its startup went with one line on a pipe:
// "ok", or "error" followed by the error's code, target and detail.
const (
	readyOK           = "ok"
	readyErrorPrefix  = "error "
	readyFieldDivider = "\t"
	readyFieldCount   = 3 // code, target, detail
)

// EncodeReady renders the daemon's startup result as the line it sends to the
// launcher.
func EncodeReady(err error) string {
	if err == nil {
		return readyOK
	}
	var coded *Error
	if !errors.As(err, &coded) {
		coded = &Error{Code: CodeInternal, Detail: err.Error()}
	}
	detail := strings.ReplaceAll(coded.Detail, "\n", " ")
	return readyErrorPrefix + string(coded.Code) + readyFieldDivider + coded.Target + readyFieldDivider + detail
}

// DecodeReady reads the daemon's startup line. It returns nil when the daemon
// came up.
func DecodeReady(line string) error {
	if line == readyOK {
		return nil
	}
	if !strings.HasPrefix(line, readyErrorPrefix) {
		return &Error{Code: CodeDaemonFailed, Detail: line}
	}
	fields := strings.SplitN(line[len(readyErrorPrefix):], readyFieldDivider, readyFieldCount)
	for len(fields) < readyFieldCount {
		fields = append(fields, "")
	}
	return &Error{Code: Code(fields[0]), Target: fields[1], Detail: fields[2]}
}
