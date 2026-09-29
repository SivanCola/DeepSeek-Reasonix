---
owner: @SivanCola
backup: @esengine
status: active
reviewed: 2026-09-29
---

# Skills

Reasonix loads [Agent Skills](https://agentskills.io): a folder with a
`SKILL.md` file whose frontmatter names and describes the skill and whose body
is the playbook. Skills written for other agents work unchanged.

To write and verify a skill from an empty directory, follow the
[community author guide](MARKET_AUTHOR_GUIDE.md). It also shows how to package
a skill and submit a fixed version for review.

## Where skills are found

Each root below is scanned for `<name>/SKILL.md`. On a name collision the
earlier scope wins: project, then custom, then global, then built-in.

| Scope | Directories |
| --- | --- |
| Project | `<repo>/.reasonix/skills`, `<repo>/.agents/skills`, `<repo>/.agent/skills`, `<repo>/.claude/skills` |
| Custom | every entry in `[skills] paths` |
| Global | `<Reasonix home>/skills` (`~/.reasonix` on macOS/Linux, `%APPDATA%\reasonix` on Windows, or `$REASONIX_HOME`), `~/.reasonix/skills`, `~/.agents/skills`, `~/.agent/skills`, `~/.claude/skills` |
| Built-in | shipped with Reasonix (`explore`, `research`, `review`, `security-review`, …) |

Symlinked skill folders are followed. Under `.claude` roots a flat `<name>.md`
file also loads, but only when it carries skill frontmatter.

Installers that target `.reasonix/skills` — for example
`npx skills add <repo> -a reasonix` — land in a directory Reasonix already scans.

## How a skill runs

Only each skill's `name` and `description` enter the system prompt; the body is
read when the skill is invoked, so an unused skill costs one line of context.

- The model invokes a skill with the `run_skill` tool.
- You invoke one by typing `/<name>` (plugin skills are `/<plugin>:<name>`).
- Markdown files in the skill's `references/` folder are appended to the body,
  and the scripts in its `scripts/` folder are listed so the model can run them.
- The result carries the absolute path of the `SKILL.md`, so any other file in
  the folder can be read from there.

## Frontmatter

`name` and `description` are the Agent Skills fields. Reasonix also reads:

| Key | Meaning |
| --- | --- |
| `allowed-tools` | Tools a subagent skill may use. |
| `runAs` | `inline` (default): the body joins the current turn. `subagent`: the skill runs in an isolated child loop and only its final answer returns. Claude-style `context: fork` or `agent:` also selects `subagent`. |
| `model`, `effort` | Model and reasoning effort for a subagent skill. |
| `read-only` | Run a subagent skill with writer tools removed and read-only shell. |
| `invocation` | `manual` keeps the skill out of the model's index; it stays callable by name. |
| `requires` | Capabilities the skill needs, e.g. `mcp-server:github`. |

Unknown keys are ignored, so a skill written for another agent loads as-is.

## Managing skills

- `/skills` lists every loaded skill with its scope and path.
- `/skills disable <name>` and `/skills enable <name>` hide or restore a skill in
  this project; add `--global` to apply everywhere.
- `reasonix doctor` reports skill health warnings, such as a missing
  description or a required capability that is not available.

```toml
[skills]
paths = ["~/my-skills", "../shared/skills"]   # extra roots
excluded_paths = ["~/.agents/skills"]         # skip a convention root
disabled_skills = ["review"]                  # hidden until /skills enable
```

Skills also arrive inside plugin packages; see [PLUGIN_PACKAGES.md](PLUGIN_PACKAGES.md).
