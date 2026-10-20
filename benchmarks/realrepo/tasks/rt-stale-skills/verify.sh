#!/usr/bin/env bash
set -e
export PYTHONPYCACHEPREFIX="$(mktemp -d)"
python3 -I - <<'PY'
import hashlib, pathlib, re, sys

root = pathlib.Path(".")
MANIFEST = """
bb32ebbcfd7c1602ececdce82fe38ec47f304c57b92b09e6d5c30fd2745436f0  Makefile
50a91e2a965931ca9d3ebab65ab61669a941c5d6b953148aa594de9dfd0c15d3  REASONIX.md
477935fb2b89905e0df115ac445f9208e37533d5c7764f5faac1048e253a91ec  cmd/benchctl/main.go
ca7b59e0f18e57b029623b132751a42146ed48791a2e6d9e5fd4be959b227e30  cmd/benchctl/main_test.go
5426039ebaa485f4a63d69deb8b702de5d707058bde6e2eb8981592ad06c1ff8  cmd/benchctl/tariff.go
960b18aac8d0b0fb1f6b27f9bf360ff643fdef3d47c03913cc4b5d7eec2fdc28  go.mod
da0097d5c73748aeca91eec12026c75c551cdd08aac266641b502fbf98fde591  internal/base/neterr/errno_unix.go
aceba80eaca9f5ba816a62bc2735620971730a33262b04d3dbd78812c325c0d5  internal/base/neterr/errno_windows.go
2dff5a6398c31a966856dea16069f695d0d98adda573eb4472fe216c051bb4b4  internal/base/neterr/neterr.go
80dd30a9542914601c0627281d297221edd1296ca39fb2191d54032dd361fce3  internal/base/neterr/neterr_test.go
0964e4c08aca91bcf7d7f180841dff3e87e3d43fa3357f662e63a5560c6b160b  internal/base/nilutil/nil.go
12298d3de24b0c50672cad4bfaae6ca71945d3fefb782a20ba5ec497407ee1ed  internal/base/nilutil/nil_test.go
ac6bba372d1a3b6a5b71b15418dee130d7ce8507da82d2c2d70cf05e75fc1d51  internal/base/retrieval/bm25.go
1f3e98147a79e3a62d6c3fb3312a4976b256dceeca37cd2513c52c26559a6b6f  internal/base/retrieval/bm25_test.go
5fdd4dfd02ea208703b555f26f6cb9cbf1d51123526d12c45399d04df734aa3a  internal/base/retrieval/v2.go
1258bb3fb6e229934b4d7638c09ad5b5a78a90acd2d13c6e3065dbc97f0011f3  internal/base/retrieval/v2_test.go
58577fbd36068033cb93dd76b9f266c082cc7e1a476420e87cd90b5d319d0877  internal/base/rulelist/doc.go
ed84bed2b3a45f255cf560b82a764ecfd0d6603f065376dc64a772a98ed0f952  internal/base/rulelist/rulelist.go
b972572713e6186c9351ec1891cf0352d707555846e2d8c7315a79126838d978  internal/base/rulelist/rulelist_test.go
91d9d39a298285e552161596932b65c44f2cabba01c0dd07c3638d616609acad  internal/base/tokencount/tokencount.go
2bf4b8bb03326a9e3bdfc3b10e27bde1002dc36ffb83b9f444fe59a38f6f019a  internal/base/tokencount/tokencount_test.go
37b3be94ee27ae709fcc3e017e32e317fd09531196927cc3da71ae4cc5649563  internal/contract/ablation/ablation.go
4c7d87823196a5c02b44871644905e52e7de12988462e0c8669ff4934b539f14  internal/contract/ablation/ablation_test.go
7f3f3b51523adab162d0e311a8391369616fa2ea701c68f66311bd6a909eadc3  internal/contract/agentgraph/doc.go
a961404feac3d04dab860c7953c05b73a9a776c3e413fa520d49ea0b643d1a15  internal/contract/agentgraph/graph.go
0c6776d078eea5dc9e61e36510e38d16d13db9391ae61fee2c3f7e36a94c3727  internal/contract/agentgraph/graph_test.go
fa3e92aa0338898aeb1561306dce248d9363ffb317f1871e4378663e20173473  internal/contract/pricing/catalog.go
50afae1ef82f5fbdf4384e458a8b0d75d0dd132e3ec4e347d786bee7037fb5af  internal/contract/pricing/catalog_test.go
5cfccacbec6c5f404eddfa511d35596d0b3ec50ba0a6561073096d0c152c8778  internal/contract/pricing/coverage.go
677639f5dec78941d629999a17b255d20277146a8e06f0a9ebb2f980ed09339c  internal/contract/pricing/coverage_test.go
497c41072813d0b0c6449a3370d116844c448ee5da386ec16ff41e0264aff8aa  internal/contract/pricing/google_rates_test.go
8be620e7ecef0140528c2a8f1cd2f2bc8debcb36abf5589d44bfd60de39fc0df  internal/contract/pricing/ledger.go
dbcec9392cd16d812ff3164ffc8ebbf8501f4936366b8b6025832b097b1e1ae0  internal/contract/pricing/migrate.go
e7b405aafdf4f2c552dcf67d5c229ee43099303bfeae61fd091a6415a19b5d1b  internal/contract/pricing/migrate_test.go
fb444233a096df20950fd0f5dba03a1a219a71977102af3de96b22a9262d3007  internal/contract/pricing/mimo_rates_test.go
907db0303ced312b1cb8be4562281a2c16e2f0bde44f128a7096a4c4f5e168d0  internal/contract/pricing/money.go
a62178dd636af9c4195c99b713e1adb4bf6d515cdcd4d65a1135783da00b39ff  internal/contract/pricing/money_test.go
4f7553725655898fac41f87e3d6b323a6d787686c36fd89ab163dbc765c3ca5d  internal/contract/pricing/official_card_test.go
5bb615829507cd6a50c9227543a5c422d340aa1e0bce969d70160988699cc0b2  internal/contract/pricing/quote.go
481acaa4067fe730a6848645cfa779a47c84eb1f38329a192200eb10589891d1  internal/contract/pricing/quote_test.go
510f68c054020fe15a419a08f75ebbf3718202a6d12806788a9f6732eba80a42  internal/contract/pricing/rates.go
b51c1ea4cd5dfe8bf5f9568c91f52054453652ebb9bda62aa2466077ac9afd24  internal/contract/pricing/rates_test.go
ac082ed4d978537ef6f941d5c64f5e65b81f8009e3396f8246f4207c018057a6  internal/contract/pricing/request_rates.go
47cba94710aa4f291e59b617f255341c27a5a5bde4e34b29c03941e46ee98029  internal/contract/pricing/request_rates_test.go
fd98c919fcc8888bb30d22355168524bd6aba2402cbb27efe040ac8b79e07aa7  internal/contract/pricing/tariff.go
a267d5d465bdfab8694bfb357ceb4ed9867564cc3719d5d7d1aa6a9d1bca97cd  internal/contract/pricing/tariff_test.go
8fa0a5b69331aca22ab06e21af9e3677db6e9251885cad39368b9f3960a00126  internal/contract/pricing/window.go
f914a434c6b1d9c0aeb3f7405622ed5e77ce42d60cdcf3cf63ad809799a4c912  internal/contract/pricing/window_test.go
"""
changed = [path for digest, path in (line.split(None, 1) for line in MANIFEST.splitlines() if line.strip())
           if not (root / path).is_file() or hashlib.sha256((root / path).read_bytes()).hexdigest() != digest]
if changed:
    print("a non-documentation file changed: " + ", ".join(changed))
    sys.exit(1)
go_files = [p for p in root.rglob("*.go") if ".reasonix" not in p.parts]
code = {p: p.read_text(encoding="utf-8") for p in go_files}
make_targets = set(re.findall(r"^([A-Za-z][\w-]*):", (root / "Makefile").read_text(encoding="utf-8"), re.M))
problems = []

def declared_top(pkg, name):
    pat = re.compile(r"^(func %s\(|type %s\b|var %s\b|const %s\b|\t%s\b)" % ((re.escape(name),) * 5), re.M)
    return any(f"package {pkg}\n" in src and pat.search(src) for src in code.values())

def method(recv, name):
    pat = re.compile(r"^func \(\w+ \*?%s\) %s\(" % (re.escape(recv), re.escape(name)), re.M)
    field = re.compile(r"^type %s struct \{[^}]*^\t%s\b" % (re.escape(recv), re.escape(name)), re.M | re.S)
    return any(pat.search(s) or field.search(s) for s in code.values())

def mentioned(name):
    pat = re.compile(r"\b%s\b" % re.escape(name))
    return any(pat.search(re.sub(r"//.*", "", s)) for s in code.values())

bench_src = "\n".join(src for p, src in code.items() if p.parts[:2] == ("cmd", "benchctl"))
subcommands = set(re.findall(r'case "([a-z]+)":', bench_src))
bench_flags = set(re.findall(r'fs\.\w+\("([\w-]+)"', bench_src)) | {"h", "help"}

def benchctl_ok(args):
    words = [w for w in args.split() if "<" not in w]
    sub = next((w for w in words if not w.startswith("-")), None)
    if sub is not None and sub not in subcommands:
        return False
    return all(w.lstrip("-").split("=")[0] in bench_flags for w in words if w.startswith("-"))

def resolves(token):
    token = token.strip()
    m = re.match(r"(?:go run \./cmd/)?benchctl(?:\s+(.*))?$", token)
    if m:
        return benchctl_ok(m.group(1) or "")
    if token.startswith("make "):
        return all(t in make_targets for t in token.split()[1:])
    if "<" in token or " " in token or token.startswith("-"):
        return True
    if "*" in token:
        return any(True for _ in root.rglob(token.rsplit("/", 1)[-1]))
    if "/" in token:
        return (root / token.rstrip("/")).exists()
    if re.search(r"\.(go|md|mod)$", token):
        return any(p.name == token for p in root.rglob(token))
    m = re.fullmatch(r"([A-Za-z_]\w*)(?:\.([A-Za-z_]\w*))?(?:\(\))?", token)
    if not m:
        return True
    head, tail = m.group(1), m.group(2)
    if tail is None:
        return mentioned(head)
    if head[0].islower():
        return declared_top(head, tail)
    return method(head, tail)

stale = {
    "price-change": ["internal/pricing/", "officialPrices", "LatestRate", "price-check"],
    "ablation-arm": ["internal/ablation/", "modules.go", "AllModules", "ParseSpec", "Set.Name", "without-", "cmd/bench/"],
    "benchctl": ["Unix seconds"],
}
required = {"price-change": [r"(?i)\bappend", r"SupersededRates", r"`From\b"], "ablation-arm": [r"\bno-", r"\bModules\b"],
            "benchctl": [r"benchctl tariff", r"RFC ?3339", r"arm -list", r"benchctl cost"]}

for name in stale:
    path = root / ".reasonix" / "skills" / name / "SKILL.md"
    if not path.exists():
        problems.append(f"{path} is missing")
        continue
    text = path.read_text(encoding="utf-8")
    fm = re.match(r"---\n(.*?)\n---\n", text, re.S)
    if not fm or not re.search(r"^name:\s*%s\s*$" % re.escape(name), fm.group(1), re.M) \
            or not re.search(r"^description:\s*\S", fm.group(1), re.M):
        problems.append(f"{name}: frontmatter lost its name or description")
    body = text[fm.end():] if fm else text
    if not re.search(r"^\s*1\.\s", body, re.M):
        problems.append(f"{name}: the procedure is no longer a numbered list")
    for token in re.findall(r"`([^`\n]+)`", body):
        if not resolves(token):
            problems.append(f"{name}: `{token}` names nothing in this repository")
    for word in stale[name]:
        if word in text:
            problems.append(f"{name}: still says {word}")
    for pat in required[name]:
        if not re.search(pat, body):
            problems.append(f"{name}: procedure does not match the code ({pat})")

readme = (root / "README.md").read_text(encoding="utf-8")
skills = readme.split("## Skills", 1)[-1]
for name in stale:
    if not re.search(r"^\|\s*`%s`\s*\|" % re.escape(name), skills, re.M):
        problems.append(f"README's skills table has no row for {name}")

for p in problems:
    print(p)
sys.exit(1 if problems else 0)
PY
