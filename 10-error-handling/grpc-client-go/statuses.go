package main

import (
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// What a client should do with each status code GemFire for gRPC (or grpc-go itself) returns.
// The code says which kind of problem it is; for RESOURCE_EXHAUSTED only the message tells the two
// causes apart.
const (
	actionRetry      = "retry: transient, try again (with backoff)"
	actionBackoff    = "back off, then retry: the server is low on memory"
	actionFixRequest = "fix the request: retrying sends the same mistake"
	actionShrink     = "fix the request: split it, it's over the message size limit"
	actionDeadline   = "check the deadline, or make the work smaller"
	actionGiveUp     = "give up and report it: a bug or an unsupported case"
	actionCredential = "fix the credentials or permissions (08)"
	actionNone       = "nothing: the client cancelled it"
)

// classify returns what to do about a failed call.
func classify(err error) string {
	st := status.Convert(err)
	switch st.Code() {
	case codes.OK:
		return ""
	case codes.Unavailable:
		return actionRetry
	case codes.ResourceExhausted:
		if strings.Contains(st.Message(), "exceeds maximum size") || strings.Contains(st.Message(), "larger than max") {
			return actionShrink
		}
		return actionBackoff
	case codes.InvalidArgument, codes.FailedPrecondition:
		return actionFixRequest
	case codes.DeadlineExceeded:
		return actionDeadline
	case codes.Unauthenticated, codes.PermissionDenied:
		return actionCredential
	case codes.Canceled:
		return actionNone
	default: // INTERNAL, UNIMPLEMENTED, UNKNOWN, ...
		return actionGiveUp
	}
}

// httpStatus maps a gRPC status to the HTTP status the /crud API answers with, so k6 and curl can
// tell the kinds of failure apart.
func httpStatus(err error) int {
	switch status.Code(err) {
	case codes.OK:
		return 200
	case codes.InvalidArgument, codes.FailedPrecondition:
		return 400
	case codes.Unauthenticated:
		return 401
	case codes.PermissionDenied:
		return 403
	case codes.ResourceExhausted:
		return 429
	case codes.Unavailable:
		return 503
	case codes.DeadlineExceeded:
		return 504
	default:
		return 500
	}
}
