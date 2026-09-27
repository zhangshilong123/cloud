package controlgrpc

import (
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/wanglongan587/cloud/internal/controlpb"
	"github.com/wanglongan587/cloud/internal/core"
)

// toStatus maps one Fault to the gRPC status the contract promises: the status code carries the
// primary classification and ErrorDetail refines it, both decided here so they never disagree.
// Database failures become UNAVAILABLE because nothing was committed and the same submission may
// be retried; their detail never reaches the caller.
func toStatus(err error) error {
	fault := core.ErrorCode(err)
	var code codes.Code
	var detail controlpb.ErrorCode
	// message defaults to the stable fault class; only the database-failure default below swaps it
	// for the opaque "persistence unavailable", so signing_failed keeps its own class name.
	message := fault.Code
	switch {
	case fault.Code == "lease_held":
		code, detail = codes.FailedPrecondition, controlpb.ErrorCode_ERROR_CODE_LEASE_HELD
	case fault.Code == "stale_controller" || fault.Code == "stale_operation":
		code, detail = codes.FailedPrecondition, controlpb.ErrorCode_ERROR_CODE_STALE_CONTROLLER
	// RetrievalCapability (Step 5B ADR D32) issuance taxonomy. Classified by code so they take
	// precedence over the generic status cases below and never leak provider detail.
	case fault.Code == "storage_not_configured":
		code, detail = codes.FailedPrecondition, controlpb.ErrorCode_ERROR_CODE_SKILL_STORAGE_NOT_CONFIGURED
	case fault.Code == "attempt_not_eligible":
		code, detail = codes.FailedPrecondition, controlpb.ErrorCode_ERROR_CODE_SKILL_ATTEMPT_NOT_ELIGIBLE
	case fault.Code == "revision_not_bound":
		code, detail = codes.NotFound, controlpb.ErrorCode_ERROR_CODE_SKILL_REVISION_NOT_BOUND
	case fault.Code == "authorization_failed":
		code, detail = codes.NotFound, controlpb.ErrorCode_ERROR_CODE_SKILL_AUTHORIZATION_FAILED
	case fault.Code == "signing_failed":
		code, detail = codes.Unavailable, controlpb.ErrorCode_ERROR_CODE_SKILL_SIGNING_FAILED
	case fault.Code == "invalid_locator":
		code, detail = codes.Internal, controlpb.ErrorCode_ERROR_CODE_SKILL_INVALID_LOCATOR
	case fault.Status == 409:
		code, detail = codes.Aborted, controlpb.ErrorCode_ERROR_CODE_CONFLICT
	case fault.Status == 400:
		code, detail = codes.InvalidArgument, controlpb.ErrorCode_ERROR_CODE_INVALID_INPUT
	case fault.Status == 403:
		code, detail = codes.PermissionDenied, controlpb.ErrorCode_ERROR_CODE_SERVICE_FORBIDDEN
	case fault.Status == 404:
		code, detail = codes.NotFound, controlpb.ErrorCode_ERROR_CODE_NOT_FOUND
	default:
		code, detail = codes.Unavailable, controlpb.ErrorCode_ERROR_CODE_UNAVAILABLE
		message = "persistence unavailable"
	}
	withDetail, e := status.New(code, message).WithDetails(&controlpb.ErrorDetail{Code: detail})
	if e != nil {
		return status.Error(code, message)
	}
	return withDetail.Err()
}
