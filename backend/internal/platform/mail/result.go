package mail

import "errors"

// NotAcceptedError is evidence that SMTP did not accept a message. Untyped
// failures are deliberately uncertain; callers must never retry them blindly.
type NotAcceptedError struct{ Err error }

func (e *NotAcceptedError) Error() string { return e.Err.Error() }
func (e *NotAcceptedError) Unwrap() error { return e.Err }

func NotAccepted(err error) error {
	if err == nil {
		return nil
	}
	return &NotAcceptedError{Err: err}
}

func DefinitelyNotAccepted(err error) bool {
	var target *NotAcceptedError
	return errors.Is(err, ErrDisabled) || errors.As(err, &target)
}
