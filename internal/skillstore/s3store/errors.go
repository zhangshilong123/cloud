package s3store

import (
	"context"
	"errors"
	"net"
	"net/http"

	"github.com/wanglongan587/cloud/internal/skillstore"

	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// class is the provider-neutral error class derived from an SDK error. It is the
// only input to the outcome mappers; raw SDK errors never cross the port (ADR D7).
type class uint8

const (
	classNotFound class = iota
	classAlreadyExists
	classTemporary
	classPermanent
	classAmbiguous
)

// classify maps an SDK error to the frozen taxonomy (ADR D6/D7). Order matters: a
// real HTTP response is the most specific signal; without one, the error is a
// transport failure and must be split into "provably no effect" (DNS, connection
// refused — the connection never existed) versus "may have been accepted"
// (established then broken, deadline, cancel), which defaults to ambiguous. The
// bias is toward ambiguous: misclassifying ambiguous as definite leads to a blind
// retry that can mask a MISMATCH, while the reverse only costs one harmless probe.
func classify(err error) class {
	if code := httpStatus(err); code != 0 {
		switch {
		case code == http.StatusNotFound:
			return classNotFound
		case code == http.StatusPreconditionFailed:
			return classAlreadyExists
		case code == http.StatusTooManyRequests:
			return classTemporary
		case code >= 500:
			return classAmbiguous
		default:
			// 400/401/403 and other 4xx: provably rejected before acceptance.
			return classPermanent
		}
	}

	// No HTTP response: transport failure.
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return classTemporary
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if opErr.Op == "dial" {
			return classTemporary // connection never established
		}
		return classAmbiguous // established, then broke mid-flight
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return classAmbiguous
	}
	return classAmbiguous
}

// httpStatus extracts the HTTP status code from an aws-sdk-go-v2 error. The SDK
// wraps every non-2xx response in a *smithy.OperationError carrying a
// *smithyhttp.ResponseError, so this is reachable for both typed errors
// (NoSuchKey, AccessDenied) and the generic error. A zero value means "no server
// response", i.e. a transport failure.
func httpStatus(err error) int {
	var respErr *smithyhttp.ResponseError
	if errors.As(err, &respErr) && respErr.Response != nil && respErr.Response.Response != nil {
		return respErr.Response.StatusCode
	}
	return 0
}

func putOutcome(c class) skillstore.PutOutcome {
	switch c {
	case classAlreadyExists:
		return skillstore.PutAlreadyExists
	case classTemporary:
		return skillstore.PutDefiniteFailureTransient
	case classAmbiguous:
		return skillstore.PutAmbiguous
	default:
		// classNotFound (unreachable on Put) and classPermanent.
		return skillstore.PutDefiniteFailurePermanent
	}
}

func statOutcome(c class) skillstore.StatOutcome {
	switch c {
	case classNotFound:
		return skillstore.StatAbsent
	case classTemporary:
		return skillstore.StatDefiniteFailureTransient
	case classAmbiguous:
		return skillstore.StatIndeterminate
	default:
		// classAlreadyExists (unreachable on Stat) and classPermanent.
		return skillstore.StatDefiniteFailurePermanent
	}
}

func getOutcome(c class) skillstore.GetOutcome {
	switch c {
	case classNotFound:
		return skillstore.GetAbsent
	case classTemporary:
		return skillstore.GetDefiniteFailureTransient
	case classAmbiguous:
		return skillstore.GetIndeterminate
	default:
		// classAlreadyExists (unreachable on Get) and classPermanent.
		return skillstore.GetDefiniteFailurePermanent
	}
}
