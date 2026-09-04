# Session experience

The desktop Settings page uses one **Session experience** preference to control how work in progress is presented in a transcript.

## Modes

| Mode | While a turn is running | After the turn completes |
| --- | --- | --- |
| **Standard** | Work in progress is visible. | Completed work is collapsed by default; expand it from the message when needed. |
| **Deep** | The complete work process is shown in real time. | Completed work remains expanded; individual sections can still be collapsed manually. |

This preference applies to reasoning, tool calls, sub-task progress, work-process cards, approvals, validation, and the active turn. It changes presentation only. It does not change the selected model, reasoning strength, provider request, cost, context window, or saved transcript data.

Manual expand/collapse is a message-level reading action and is retained for the message row. It does not create another global setting.

## Configuration and compatibility

The canonical desktop configuration is:

```toml
[desktop]
session_experience = "standard"
```

The only valid values are `standard` and `deep`; missing or invalid values use `standard`. Older display preferences remain compatibility mirrors for one release cycle and are not used as the source of truth by the new Settings UI.
