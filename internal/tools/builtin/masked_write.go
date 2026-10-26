package builtin

import (
	"encoding/json"
	"fmt"
	"strings"

	"reasonix/internal/base/secrets"
	"reasonix/internal/contract/tool"
)

// CodeMaskedSecretWrite identifies a file write refused for carrying the mark a
// model reads in place of a secret the user saved.
const CodeMaskedSecretWrite = "secret.masked_write"

// refuseMaskedWrite stops a write that would put a secret's mark where the
// file holds its value: a model that read a masked copy writes the mark back.
func refuseMaskedWrite(args json.RawMessage) error {
	labels := secrets.Masker().Marks(string(args))
	if len(labels) == 0 {
		return nil
	}
	vars := make([]string, len(labels))
	for i, l := range labels {
		vars[i] = "$REASONIX_SECRET_" + l
	}
	return tool.Refusal{Code: CodeMaskedSecretWrite, Message: fmt.Sprintf(
		"write refused: the text holds a [secret:...] mark, which you read in place of a secret the user saved (%s); the file holds the real value, and writing the mark would replace it. Leave those lines as they are and edit around them; to remove one, use delete_range or a shell command. A shell command reads the value as %s, whichever you were given.",
		strings.Join(labels, ", "), strings.Join(vars, " or "))}
}
