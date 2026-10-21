//go:build linux

package checkenv

func (i *Invocation) recordReceipt() {
	input := i.state.input
	input.EnvironmentReceipt = digest(struct{ Identity, Execution, Call, Runtime, Source string }{
		i.identity, i.executionID, i.callID, i.state.runtimeDigest, i.state.sourceDigest})
	receipts.Lock()
	defer receipts.Unlock()
	if len(receipts.order) >= 4096 {
		delete(receipts.inputs, receipts.order[0])
		receipts.order = receipts.order[1:]
	}
	receipts.inputs[input.EnvironmentReceipt] = ownedReceipt{input, i.executionID}
	receipts.order = append(receipts.order, input.EnvironmentReceipt)
	i.state.input = input
}
