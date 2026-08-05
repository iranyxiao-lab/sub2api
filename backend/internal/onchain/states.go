package onchain

type IntentStatus string

const (
	IntentPending        IntentStatus = "PENDING"
	IntentPartiallyPaid  IntentStatus = "PARTIALLY_PAID"
	IntentSettlementDue  IntentStatus = "SETTLEMENT_DUE"
	IntentSettling       IntentStatus = "SETTLING"
	IntentSettled        IntentStatus = "SETTLED"
	IntentReviewRequired IntentStatus = "REVIEW_REQUIRED"
	IntentDisabled       IntentStatus = "DISABLED"
)

type DepositStatus string

const (
	DepositConfirmed      DepositStatus = "CONFIRMED"
	DepositCreditPending  DepositStatus = "CREDIT_PENDING"
	DepositCredited       DepositStatus = "CREDITED"
	DepositReviewRequired DepositStatus = "REVIEW_REQUIRED"
	DepositRejected       DepositStatus = "REJECTED"
)

type CursorHealth string

const (
	CursorHealthy      CursorHealth = "HEALTHY"
	CursorLagging      CursorHealth = "LAGGING"
	CursorNodeError    CursorHealth = "NODE_ERROR"
	CursorHashConflict CursorHealth = "HASH_CONFLICT"
	CursorPaused       CursorHealth = "PAUSED"
)

type TransferStatus string

const (
	TransferPrepared       TransferStatus = "PREPARED"
	TransferResourceWait   TransferStatus = "RESOURCE_WAIT"
	TransferFeeWait        TransferStatus = "FEE_WAIT"
	TransferSigning        TransferStatus = "SIGNING"
	TransferSigned         TransferStatus = "SIGNED"
	TransferBroadcast      TransferStatus = "BROADCAST"
	TransferConfirming     TransferStatus = "CONFIRMING"
	TransferFinalized      TransferStatus = "FINALIZED"
	TransferReplaced       TransferStatus = "REPLACED"
	TransferFailed         TransferStatus = "FAILED"
	TransferReviewRequired TransferStatus = "REVIEW_REQUIRED"
)

type NonceStatus string

const (
	NonceReady       NonceStatus = "READY"
	NonceReserved    NonceStatus = "RESERVED"
	NonceReconciling NonceStatus = "RECONCILING"
	NonceConflict    NonceStatus = "CONFLICT"
	NoncePaused      NonceStatus = "PAUSED"
)

type TransactionVersionStatus string

const (
	TransactionPrepared  TransactionVersionStatus = "PREPARED"
	TransactionSigned    TransactionVersionStatus = "SIGNED"
	TransactionBroadcast TransactionVersionStatus = "BROADCAST"
	TransactionPending   TransactionVersionStatus = "PENDING"
	TransactionReplaced  TransactionVersionStatus = "REPLACED"
	TransactionFinalized TransactionVersionStatus = "FINALIZED"
	TransactionFailed    TransactionVersionStatus = "FAILED"
	TransactionUnknown   TransactionVersionStatus = "UNKNOWN"
)

type ReviewStatus string

const (
	ReviewOpen       ReviewStatus = "OPEN"
	ReviewInProgress ReviewStatus = "IN_PROGRESS"
	ReviewApproved   ReviewStatus = "APPROVED"
	ReviewRejected   ReviewStatus = "REJECTED"
	ReviewResolved   ReviewStatus = "RESOLVED"
)

var intentTransitions = map[IntentStatus]map[IntentStatus]struct{}{
	IntentPending:        setOf(IntentPartiallyPaid, IntentSettlementDue, IntentReviewRequired, IntentDisabled),
	IntentPartiallyPaid:  setOf(IntentSettlementDue, IntentReviewRequired, IntentDisabled),
	IntentSettlementDue:  setOf(IntentSettling, IntentReviewRequired, IntentDisabled),
	IntentSettling:       setOf(IntentSettlementDue, IntentSettled, IntentReviewRequired),
	IntentSettled:        setOf(IntentReviewRequired),
	IntentReviewRequired: setOf(IntentSettlementDue, IntentDisabled),
	IntentDisabled:       setOf(IntentPending, IntentPartiallyPaid, IntentSettlementDue),
}

var depositTransitions = map[DepositStatus]map[DepositStatus]struct{}{
	DepositConfirmed:      setOf(DepositCreditPending, DepositReviewRequired, DepositRejected),
	DepositCreditPending:  setOf(DepositCredited, DepositReviewRequired),
	DepositCredited:       setOf(DepositReviewRequired),
	DepositReviewRequired: setOf(DepositCreditPending, DepositCredited, DepositRejected),
}

var cursorTransitions = map[CursorHealth]map[CursorHealth]struct{}{
	CursorHealthy:      setOf(CursorLagging, CursorNodeError, CursorHashConflict, CursorPaused),
	CursorLagging:      setOf(CursorHealthy, CursorNodeError, CursorHashConflict, CursorPaused),
	CursorNodeError:    setOf(CursorHealthy, CursorLagging, CursorHashConflict, CursorPaused),
	CursorHashConflict: setOf(CursorPaused),
	CursorPaused:       setOf(CursorHealthy, CursorLagging, CursorNodeError, CursorHashConflict),
}

var transferTransitions = map[TransferStatus]map[TransferStatus]struct{}{
	TransferPrepared:       setOf(TransferResourceWait, TransferFeeWait, TransferSigning, TransferFailed, TransferReviewRequired),
	TransferResourceWait:   setOf(TransferPrepared, TransferReviewRequired),
	TransferFeeWait:        setOf(TransferPrepared, TransferReviewRequired),
	TransferSigning:        setOf(TransferSigned, TransferFailed, TransferReviewRequired),
	TransferSigned:         setOf(TransferBroadcast, TransferConfirming, TransferFailed, TransferReviewRequired),
	TransferBroadcast:      setOf(TransferConfirming, TransferReplaced, TransferFailed, TransferReviewRequired),
	TransferConfirming:     setOf(TransferFinalized, TransferFailed, TransferReviewRequired),
	TransferReplaced:       setOf(TransferFinalized, TransferFailed, TransferReviewRequired),
	TransferFailed:         setOf(TransferPrepared, TransferReviewRequired),
	TransferReviewRequired: setOf(TransferPrepared, TransferFailed),
}

var nonceTransitions = map[NonceStatus]map[NonceStatus]struct{}{
	NonceReady:       setOf(NonceReserved, NonceReconciling, NoncePaused),
	NonceReserved:    setOf(NonceReady, NonceReconciling, NonceConflict, NoncePaused),
	NonceReconciling: setOf(NonceReady, NonceConflict, NoncePaused),
	NonceConflict:    setOf(NonceReconciling, NoncePaused),
	NoncePaused:      setOf(NonceReconciling, NonceReady),
}

var transactionTransitions = map[TransactionVersionStatus]map[TransactionVersionStatus]struct{}{
	TransactionPrepared:  setOf(TransactionSigned, TransactionFailed),
	TransactionSigned:    setOf(TransactionBroadcast, TransactionUnknown, TransactionFailed),
	TransactionBroadcast: setOf(TransactionPending, TransactionFinalized, TransactionUnknown, TransactionFailed, TransactionReplaced),
	TransactionPending:   setOf(TransactionFinalized, TransactionFailed, TransactionReplaced, TransactionUnknown),
	TransactionUnknown:   setOf(TransactionPending, TransactionFinalized, TransactionFailed, TransactionReplaced),
	TransactionReplaced:  setOf(TransactionFinalized, TransactionFailed),
}

var reviewTransitions = map[ReviewStatus]map[ReviewStatus]struct{}{
	ReviewOpen:       setOf(ReviewInProgress, ReviewApproved, ReviewRejected),
	ReviewInProgress: setOf(ReviewApproved, ReviewRejected, ReviewOpen),
	ReviewApproved:   setOf(ReviewResolved),
	ReviewRejected:   setOf(ReviewResolved),
}

func CanTransitionIntent(from, to IntentStatus) bool {
	return canTransition(intentTransitions, from, to)
}

func CanTransitionDeposit(from, to DepositStatus) bool {
	return canTransition(depositTransitions, from, to)
}

func CanTransitionCursor(from, to CursorHealth) bool {
	return canTransition(cursorTransitions, from, to)
}

func CanTransitionTransfer(from, to TransferStatus) bool {
	return canTransition(transferTransitions, from, to)
}

func CanTransitionNonce(from, to NonceStatus) bool {
	return canTransition(nonceTransitions, from, to)
}

func CanTransitionTransaction(from, to TransactionVersionStatus) bool {
	return canTransition(transactionTransitions, from, to)
}

func CanTransitionReview(from, to ReviewStatus) bool {
	return canTransition(reviewTransitions, from, to)
}

func canTransition[T comparable](transitions map[T]map[T]struct{}, from, to T) bool {
	next, ok := transitions[from]
	if !ok {
		return false
	}
	_, ok = next[to]
	return ok
}

func setOf[T comparable](values ...T) map[T]struct{} {
	result := make(map[T]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}
