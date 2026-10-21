//go:build linux

package checkenv

func (s Spec) captureIdentity() string {
	if s.capture == nil {
		return ""
	}
	return digest(capturedInputIdentity{s.capture.path, s.capture.body})
}

func (i *Invocation) recordReceipt() {
	input := i.state.input
	input.EnvironmentReceipt = digest(struct{ Identity, Execution, Call, Runtime, Source, Capture string }{
		i.identity, i.executionID, i.callID, i.state.runtimeDigest, i.state.sourceDigest, i.spec.captureIdentity()})
	receipts.Lock()
	defer receipts.Unlock()
	if len(receipts.order) >= 4096 {
		delete(receipts.inputs, receipts.order[0])
		receipts.order = receipts.order[1:]
	}
	receipts.inputs[input.EnvironmentReceipt] = ownedReceipt{input, i.executionID, i.spec.captureIdentity()}
	receipts.order = append(receipts.order, input.EnvironmentReceipt)
	i.state.input = input
}
