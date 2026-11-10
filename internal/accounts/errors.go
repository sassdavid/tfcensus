package accounts

import (
	"errors"
	"fmt"

	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

var (
	// ErrNoProfile is returned when a --profile glob matches no profile.
	ErrNoProfile = errors.New("no such profile")
	// ErrAuth marks errors caused by missing, expired or rejected credentials.
	ErrAuth = errors.New("authentication failed")
	// ErrAccessDenied marks an API call that the credentials may not make.
	ErrAccessDenied = errors.New("access denied")
)

// AuthError is a credential failure for one account source.
type AuthError struct {
	Source     string // the profile, or the profile a role was assumed from
	Profile    string
	Role       string // the role that could not be assumed
	SSO        bool   // the profile signs in through IAM Identity Center
	SSOSession string // set when the profile uses an [sso-session] section
	Err        error
}

func (e *AuthError) Error() string {
	if e.Role != "" {
		if IsAccessDenied(e.Err) {
			return fmt.Sprintf("cannot assume %s from %s: access denied", e.Role, e.Source)
		}
		return fmt.Sprintf("cannot assume %s from %s: %v", e.Role, e.Source, e.Err)
	}
	if e.SSO {
		login := "aws sso login --profile " + e.Profile
		if e.SSOSession != "" {
			login = "aws sso login --sso-session " + e.SSOSession
		}
		return fmt.Sprintf("SSO token for profile %s is expired or missing. Run `%s`", e.Profile, login)
	}
	return fmt.Sprintf("no usable credentials for %s: %v", e.Source, e.Err)
}

func (e *AuthError) Unwrap() []error { return []error{ErrAuth, e.Err} }

// IsAccessDenied reports whether err is an AWS authorization failure for one API call.
func IsAccessDenied(err error) bool {
	if errors.Is(err, ErrAccessDenied) {
		return true
	}
	ae, ok := errors.AsType[smithy.APIError](err)
	if !ok {
		return false
	}
	switch ae.ErrorCode() {
	case "AccessDenied", "AccessDeniedException", "UnauthorizedOperation", "UnauthorizedException",
		"AuthorizationError":
		return true
	}
	return false
}

// isExpiredCredentials reports whether AWS rejected the credentials themselves, for example
// expired session credentials, rather than one action.
func isExpiredCredentials(err error) bool {
	ae, ok := errors.AsType[smithy.APIError](err)
	if !ok {
		return false
	}
	switch ae.ErrorCode() {
	case "ExpiredToken", "ExpiredTokenException", "InvalidClientTokenId", "UnrecognizedClientException",
		"InvalidAccessKeyId", "SignatureDoesNotMatch", "RequestExpired":
		return true
	}
	return false
}

// isNetwork reports whether err happened before AWS answered, such as a DNS or TLS failure.
func isNetwork(err error) bool {
	_, ok := errors.AsType[*smithyhttp.RequestSendError](err)
	return ok
}
