// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package grpc

import (
	"net/http"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestToStatus_Mapping(t *testing.T) {
	cases := []struct {
		httpStatus int
		wantCode   codes.Code
	}{
		{http.StatusNotFound, codes.NotFound},
		{http.StatusBadRequest, codes.InvalidArgument},
		{http.StatusUnprocessableEntity, codes.FailedPrecondition},
		{http.StatusInternalServerError, codes.Internal},
	}

	for _, tc := range cases {
		err := toStatus(tc.httpStatus, "some message")
		if err == nil {
			t.Errorf("httpStatus=%d: expected non-nil error", tc.httpStatus)
			continue
		}
		st, ok := status.FromError(err)
		if !ok {
			t.Errorf("httpStatus=%d: not a gRPC status error", tc.httpStatus)
			continue
		}
		if st.Code() != tc.wantCode {
			t.Errorf("httpStatus=%d: want %s, got %s", tc.httpStatus, tc.wantCode, st.Code())
		}
	}
}

func TestToStatus_MessagePreserved(t *testing.T) {
	msg := "item not found"
	err := toStatus(http.StatusNotFound, msg)
	st, _ := status.FromError(err)
	if st.Message() != msg {
		t.Errorf("message: want %q, got %q", msg, st.Message())
	}
}

func TestToStatus_UnknownHTTPStatusFallsBackToInternal(t *testing.T) {
	err := toStatus(http.StatusTeapot, "I'm a teapot")
	st, _ := status.FromError(err)
	if st.Code() != codes.Internal {
		t.Errorf("unknown HTTP status: want Internal, got %s", st.Code())
	}
}
