package myprs

// The sentinel errors the e_ tests compare with errors.Is.
var (
	ErrENotRepoName  = errNotRepoName
	ErrENotNumber    = errNotNumber
	ErrENotThreadID  = errNotThreadID
	ErrENotReplyText = errNotReplyText
	ErrENotConfirmed = errNotConfirmed
	ErrENotPRURL     = errNotPRURL
	ErrENotRound     = errNotRound
	ErrENotLogin     = errNotLogin
	ErrENotPattern   = errNotPattern
	ErrERemoveSome   = errRemoveSome
	ErrENotJSON      = errNotJSON
	ErrENoSearch     = errNoSearch
	ErrENoThread     = errNoThread
	ErrENotARound    = errNotARound
	ErrENameTooLong  = errNameTooLong
	ErrEUnsafeRef    = errUnsafeRef
)
