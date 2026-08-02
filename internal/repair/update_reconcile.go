package repair

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
)

// UpdateRecoveryState classifies a pending update transaction without guessing:
// the transaction file, the installed release-unit state, the failure marker,
// the running version, and the OS lock together determine the state.
type UpdateRecoveryState string

const (
	UpdateRecoveryNone          UpdateRecoveryState = "none"
	UpdateRecoveryPrepared      UpdateRecoveryState = "prepared"
	UpdateRecoveryProbationary  UpdateRecoveryState = "probationary"
	UpdateRecoveryFailedInstall UpdateRecoveryState = "failed_install"
	UpdateRecoveryRestored      UpdateRecoveryState = "restored"
	UpdateRecoveryActiveHandoff UpdateRecoveryState = "active_handoff"
	UpdateRecoveryBlocked       UpdateRecoveryState = "blocked"
)

// UpdateRecoveryView is the content-free reconciliation result surfaced by the
// updater UI: an explicit state, message and action instead of a generic
// "a pending update already exists".
type UpdateRecoveryView struct {
	State       UpdateRecoveryState `json:"state"`
	FromVersion string              `json:"fromVersion,omitempty"`
	ToVersion   string              `json:"toVersion,omitempty"`
	Message     string              `json:"message,omitempty"`
	Action      string              `json:"action,omitempty"` // commit|cancel|rollback|wait|none
	Retryable   bool                `json:"retryable,omitempty"`
}

func updateRecoveryView(state UpdateRecoveryState, tx *UpdateTransaction, message, action string, retryable bool) UpdateRecoveryView {
	view := UpdateRecoveryView{State: state, Message: message, Action: action, Retryable: retryable}
	if tx != nil {
		view.FromVersion = tx.FromVersion
		view.ToVersion = tx.ToVersion
	}
	return view
}

// InspectPendingUpdate derives the current pending-update state read-only. It
// never mutates files and never takes the pending-update lock; callers that
// act on the state must use ReconcilePendingUpdate so the exact transaction
// observed here is re-verified under the OS lock.
func InspectPendingUpdate(runningVersion string) (UpdateRecoveryView, error) {
	path := PendingUpdatePath()
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// No transaction: a failure marker without a complete transaction
			// is stale and never authorizes rollback, but the next launch can
			// still surface the installer failure.
			if failure, ok := ReadUpdateApplyFailure(); ok && strings.TrimSpace(failure.UpdateTransactionID) != "" {
				return updateRecoveryView(UpdateRecoveryFailedInstall, nil,
					"the update installer failed; the previous release will be restored on the next launch", "rollback", true), nil
			}
			return updateRecoveryView(UpdateRecoveryNone, nil, "", "", false), nil
		}
		return UpdateRecoveryView{}, err
	}
	var tx UpdateTransaction
	if json.Unmarshal(b, &tx) != nil {
		return updateRecoveryView(UpdateRecoveryBlocked, nil,
			"the pending update transaction is corrupt; run reasonix-guard diagnose or reinstall", "none", false), nil
	}
	if err := validateUpdateTransaction(&tx); err != nil {
		return updateRecoveryView(UpdateRecoveryBlocked, &tx,
			"the pending update transaction is invalid: "+err.Error(), "none", false), nil
	}

	// A failure marker bound to this transaction takes precedence: the
	// installer reported failure and the previous release must be restored.
	if failure, ok := ReadUpdateApplyFailure(); ok && updateFailureMatchesTransaction(failure, &tx) {
		return updateRecoveryView(UpdateRecoveryFailedInstall, &tx,
			"the update installer failed; the previous release will be restored", "rollback", true), nil
	}

	// An installed release-unit state means the new release was published.
	if record, stateErr := readInstalledFileUpdateState(&tx); stateErr == nil && record != nil && record.UpdateTransactionID == UpdateTransactionID(&tx) {
		switch strings.TrimSpace(runningVersion) {
		case strings.TrimSpace(tx.ToVersion):
			return updateRecoveryView(UpdateRecoveryProbationary, &tx,
				"the new version is running and will commit after a healthy start", "wait", false), nil
		case strings.TrimSpace(tx.FromVersion):
			// The old release is in place again with the install record still
			// present: the update was reverted (or the swap never launched)
			// and the transaction can end once the release unit matches its
			// backup.
			if err := verifyUpdateRestoredUnit(&tx); err != nil {
				return updateRecoveryView(UpdateRecoveryBlocked, &tx,
					"the restored release unit does not match its backup: "+err.Error(), "none", false), nil
			}
			return updateRecoveryView(UpdateRecoveryRestored, &tx,
				"the previous version is running; the pending transaction can end", "commit", false), nil
		default:
			return updateRecoveryView(UpdateRecoveryBlocked, &tx,
				"the running version cannot be attributed to this transaction", "none", false), nil
		}
	}

	// No install record: the release was not published yet.
	if tx.TargetKind == "app-bundle" && tx.HandoffOwnerPID > 0 && processAlive(tx.HandoffOwnerPID) {
		return updateRecoveryView(UpdateRecoveryActiveHandoff, &tx,
			"an update handoff process is running; wait for it to finish", "wait", false), nil
	}
	// The transaction must still be exactly reversible.
	if err := verifyUpdatePreparedState(&tx); err != nil {
		return updateRecoveryView(UpdateRecoveryBlocked, &tx,
			"the prepared update is no longer intact: "+err.Error(), "none", false), nil
	}
	return updateRecoveryView(UpdateRecoveryPrepared, &tx,
		"an update is prepared but not installed", "cancel", true), nil
}

func updateFailureMatchesTransaction(failure *UpdateApplyFailure, tx *UpdateTransaction) bool {
	if failure == nil || tx == nil {
		return false
	}
	if id := strings.TrimSpace(failure.UpdateTransactionID); id != "" {
		return id == UpdateTransactionID(tx)
	}
	return strings.TrimSpace(failure.ToVersion) == strings.TrimSpace(tx.ToVersion) &&
		strings.TrimSpace(failure.UpdateCreatedAt) == strings.TrimSpace(tx.CreatedAt)
}

// verifyUpdatePreparedState confirms the transaction is still exactly
// reversible before it is acted on.
func verifyUpdatePreparedState(tx *UpdateTransaction) error {
	switch tx.TargetKind {
	case "app-bundle":
		if err := VerifyAppBundleUpdateHandoffSource(tx); err != nil {
			return err
		}
		return VerifyAppBundleUpdateHandoffOriginal(tx)
	default:
		return verifyPreparedFileUpdateBackups(tx)
	}
}

// verifyUpdateRestoredUnit confirms the current release unit matches the
// transaction's backup (the old release is in place again).
func verifyUpdateRestoredUnit(tx *UpdateTransaction) error {
	switch tx.TargetKind {
	case "app-bundle":
		return VerifyAppBundleUpdateHandoffOriginal(tx)
	default:
		return verifyRestoredFileUpdateTargets(tx.Files)
	}
}

// EndPendingUpdateTransactionVerified ends a transaction whose release unit is
// already settled (the restored state: the running version is the previous
// release and the installed record still exists). The pending update and its
// backups are removed only after the verification callback confirms the
// release unit matches the transaction's backup, and the exact transaction
// identity still matches under the pending and target locks.
func EndPendingUpdateTransactionVerified(expected *UpdateTransaction, verify func() error) error {
	if expected == nil {
		return fmt.Errorf("end update transaction: transaction identity is incomplete")
	}
	expectedID := UpdateTransactionID(expected)
	unlock, err := acquirePendingUpdateLock()
	if err != nil {
		return fmt.Errorf("end update transaction: lock pending transaction: %w", err)
	}
	defer unlock()
	tx, err := ReadPendingUpdate()
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if expectedID != "" && UpdateTransactionID(tx) != expectedID {
		return fmt.Errorf("end update transaction: pending transaction changed")
	}
	if !reflect.DeepEqual(expected, tx) {
		return fmt.Errorf("end update transaction: pending transaction changed while waiting")
	}
	unlockTargets, lockErr := lockRepairMutations(pendingUpdateTargetPaths(tx)...)
	if lockErr != nil {
		return fmt.Errorf("end update transaction: lock targets: %w", lockErr)
	}
	defer unlockTargets()
	current, err := ReadPendingUpdate()
	if err != nil {
		return fmt.Errorf("end update transaction: re-read pending transaction: %w", err)
	}
	if !reflect.DeepEqual(tx, current) {
		return fmt.Errorf("end update transaction: pending transaction changed while waiting")
	}
	tx = current
	if err := verify(); err != nil {
		return err
	}
	if err := removePendingUpdateExactVerified(tx, verify); err != nil {
		return err
	}
	removeUpdateBackups(tx)
	_ = removeInstalledFileUpdateState(tx)
	return nil
}
