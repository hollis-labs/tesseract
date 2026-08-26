package mcpadapter

// Error-code vocabulary for MCP tool results.
//
// Every failure an MCP tool returns carries a machine-readable `code`. Before
// this file those codes were inline string literals at ~150 call sites, and a
// description could advertise a code no path ever emitted: CW-20260825-0006
// shipped `similarity_min` documented as answering `service_unavailable` where
// the path emits similarityUnavailable. Nothing could catch it, because there
// was nothing for the literal to be checked against.
//
// A symbol can be checked. errorcodes_test.go asserts, by walking this
// package's AST rather than by reading a hand-maintained list:
//
//   - every toolError call site passes one of these constants, never a literal
//     (TestEveryToolErrorCallSitePassesAnErrorCodeConstant)
//   - every constant appears at some toolError call site
//     (TestEveryErrorCodeConstantIsUsedAtAToolErrorCallSite)
//   - every code-shaped token in a shipped tool or parameter description is a
//     defined constant (TestToolDescriptionsNameOnlyDefinedErrorCodes)
//
// WHAT THIS DOES NOT ESTABLISH. A constant proves a description and an
// emission site name the SAME symbol. It says nothing about whether that
// symbol is the RIGHT code for the condition: codeValidationError could be
// emitted where codeNotFound belongs, consistently, everywhere, and every
// check here would pass. That remains a reading job.
type errorCode string

const (
	// Argument-level failures.
	codeValidationError errorCode = "validation_error"
	codeSelectorError   errorCode = "selector_error"

	// Lookup failures.
	codeNotFound      errorCode = "not_found"
	codeSkillNotFound errorCode = "skill_not_found"

	// Authorization failures.
	codeAuthRequired          errorCode = "auth_required"
	codeInsufficientScope     errorCode = "insufficient_scope"
	codeNamespaceNotPermitted errorCode = "namespace_not_permitted"

	// Capability failures — a knob was asked for that this deployment cannot
	// serve. Distinct from a validation error: the request was well-formed.
	codeDomainUnavailable     errorCode = "domain_unavailable"
	codeEmbeddingUnavailable  errorCode = "embedding_unavailable"
	codeSimilarityUnavailable errorCode = "similarity_unavailable"

	// Operation failures — the request was accepted and the work did not
	// complete. One per operation, because the caller's recovery differs.
	codeApplyFailed     errorCode = "apply_failed"
	codeApproveFailed   errorCode = "approve_failed"
	codeDeprecateFailed errorCode = "deprecate_failed"
	codePromoteFailed   errorCode = "promote_failed"
	codeRegisterFailed  errorCode = "register_failed"
	codeWriteFailed     errorCode = "write_failed"
	codeEmbeddingError  errorCode = "embedding_error"
	codeSearchError     errorCode = "search_error"

	// State and internals.
	codeInvalidState  errorCode = "invalid_state"
	codeInternalError errorCode = "internal_error"
)
