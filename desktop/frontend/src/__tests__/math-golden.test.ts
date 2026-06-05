// Golden-case verification for math rendering pipeline.
// Run: tsx src/__tests__/math-golden.test.ts
import { isInlineMathLike } from "../components/CodeOrMath";
import { stripMathDelimiters, latexNormalizeForKatex } from "../components/latexNormalize";

let passed = 0;
let failed = 0;

function check(label: string, fn: () => boolean) {
  try {
    if (fn()) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
    else      { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
  } catch (e) {
    process.stdout.write(`  ERROR ${label}: ${e}\n`); failed += 1;
  }
}

function eq(a: any, b: any, label: string) {
  if (a === b) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    // prettier-ignore
    process.stdout.write(`  FAIL  ${label}: expected ${JSON.stringify(b)}, got ${JSON.stringify(a)}\n`);
    failed += 1;
  }
}

// ── stripMathDelimiters ────────────────────────────────────────────────────────

console.log("\nstripMathDelimiters");
eq(stripMathDelimiters("\\(x+1\\)"), "x+1", "\\(...\\)");
eq(stripMathDelimiters("\\[E=mc^2\\]"), "E=mc^2", "\\[...\\]");
eq(stripMathDelimiters("$$\\frac{a}{b}$$"), "\\frac{a}{b}", "$$...$$");
eq(stripMathDelimiters("$x_i^2$"), "x_i^2", "$...$");
eq(stripMathDelimiters("plain text"), "plain text", "no delimiters");
eq(stripMathDelimiters("$a|b$"), "a|b", "inline with pipe");

// ── latexNormalizeForKatex ─────────────────────────────────────────────────────

console.log("\nlatexNormalizeForKatex");
eq(latexNormalizeForKatex("x+1"), "x+1", "plain unchanged");
eq(latexNormalizeForKatex("\\text{baryon #}"), "\\text{baryon \\#}", "escapes # in \\text");
eq(latexNormalizeForKatex("\\text{cost is $5}"), "\\text{cost is \\textdollar{}5}", "escapes $ in \\text");
eq(latexNormalizeForKatex("\\text{a & b % c_d ^ e ~ f}"),
  "\\text{a \\& b \\% c\\_d \\textasciicircum{} e \\textasciitilde{} f}",
  "escapes & % _ ^ ~ in \\text");
eq(latexNormalizeForKatex("\\text{already\\_escaped}"), "\\text{already\\_escaped}", "no double-escape");
eq(latexNormalizeForKatex("\\alpha + \\beta"), "\\alpha + \\beta", "non-text commands");
eq(latexNormalizeForKatex("a | b"), "a \\vert  b", "| to \\vert");
eq(latexNormalizeForKatex("\\text{foo \\$ bar}"), "\\text{foo \\$ bar}", "already escaped $");
eq(latexNormalizeForKatex("\\textrm{test #}"), "\\textrm{test \\#}", "\\textrm also handled");
eq(latexNormalizeForKatex("\\textbf{hello world}"), "\\textbf{hello world}", "\\textbf no special chars");
eq(latexNormalizeForKatex("\\tfrac{a}{b}"), "\\tfrac{a}{b}", "nested braces in command");

// ── isInlineMathLike (CodeOrMath classifier) ───────────────────────────────────

console.log("\nisInlineMathLike — math");
check("\\text command", () => isInlineMathLike("\\text{baryon #}") === true);
check("\\frac", () => isInlineMathLike("\\frac{a}{b}") === true);
check("\\sqrt", () => isInlineMathLike("\\sqrt{x}") === true);
check("\\alpha", () => isInlineMathLike("\\alpha + \\beta") === true);
check("\\sum", () => isInlineMathLike("\\sum_{i=1}^{n}") === true);
check("\\int", () => isInlineMathLike("\\int_0^\\infty") === true);
check("\\begin", () => isInlineMathLike("\\begin{matrix} a & b \\end{matrix}") === true);
check("sub/superscript x_i^2", () => isInlineMathLike("x_i^2") === true);
check("superscript E=mc^2", () => isInlineMathLike("E=mc^2") === true);
check("\\forall", () => isInlineMathLike("\\forall x \\in \\mathbb{R}") === true);
check("\\(x+1\\) wrapped", () => isInlineMathLike("\\(x+1\\)") === true);
check("$x+1$ wrapped", () => isInlineMathLike("$x+1$") === true);
check("\\mathbf{bold}", () => isInlineMathLike("\\mathbf{x}") === true);
check("\\mathbb{R}", () => isInlineMathLike("\\mathbb{R}") === true);
check("a \\le b", () => isInlineMathLike("a \\le b") === true);

console.log("\nisInlineMathLike — code");
check("npm run build", () => isInlineMathLike("npm run build") === false);
check("Markdown.tsx", () => isInlineMathLike("Markdown.tsx") === false);
check("const x = 1", () => isInlineMathLike("const x = 1") === false);
check("arrow =>", () => isInlineMathLike("=> x") === false);
check("equality ==", () => isInlineMathLike("a == b") === false);
check("strict ===", () => isInlineMathLike("a === b") === false);
check("SQL SELECT", () => isInlineMathLike("SELECT * FROM users") === false);
check("Go func", () => isInlineMathLike("func foo()") === false);
check("env $PATH", () => isInlineMathLike("$PATH") === false);
check("env $HOME", () => isInlineMathLike("$HOME") === false);
check("docker ps", () => isInlineMathLike("docker ps") === false);
check("git log", () => isInlineMathLike("git log") === false);
check("ssh user@host", () => isInlineMathLike("ssh user@host") === false);
check("curl URL", () => isInlineMathLike("curl https://example.com") === false);
check("3 char no math", () => isInlineMathLike("abc") === false);
check("file size 5mb", () => isInlineMathLike("5mb") === false);
check("npx cmd", () => isInlineMathLike("npx tsx") === false);
check("pnpm cmd", () => isInlineMathLike("pnpm install") === false);
check("yarn cmd", () => isInlineMathLike("yarn dev") === false);
check("import stmt", () => isInlineMathLike("import React from 'react'") === false);
check("export stmt", () => isInlineMathLike("export default App") === false);
check(".env filename", () => isInlineMathLike(".env") === false);

// ── isLikelyInlineMath (remark plugin $...$ gate) ──────────────────────────────
// Mirrors the isLikelyInlineMath logic from Markdown.tsx.

const isLikelyInlineMath = (math: string): boolean => {
  if (!math || math !== math.trim() || math.includes("\n")) return false;
  if (math.includes("://") || math.includes("](")) return false;
  if (/^\$?\d+(?:\.\d+)?%?$/.test(math)) return false;
  if (/\\[A-Za-z]+\b/.test(math)) return true;
  if (/[\^_{}|]/.test(math)) return true;
  if (/\b(?:alpha|beta|gamma|sum|int|prod|lim|infty|sqrt|frac|sin|cos|tan|log|ln|max|min|partial|nabla|left|right)\b/.test(math)) return true;
  if (/[A-Za-z]\s+[A-Za-z]/.test(math)) return false;
  return true;
};

console.log("\nisLikelyInlineMath — math");
check("$x$ (single var)", () => isLikelyInlineMath("x") === true);
check("$E=mc^2$", () => isLikelyInlineMath("E=mc^2") === true);
check("$x_i^2$", () => isLikelyInlineMath("x_i^2") === true);
check("$\\alpha$", () => isLikelyInlineMath("\\alpha") === true);
check("$a \\le b$", () => isLikelyInlineMath("a \\le b") === true);
check("$\\frac{a}{b}$", () => isLikelyInlineMath("\\frac{a}{b}") === true);
check("$f(x)$", () => isLikelyInlineMath("f(x)") === true);

console.log("\nisLikelyInlineMath — currency/link (NOT math)");
check("$5", () => isLikelyInlineMath("5") === false);
check("$10", () => isLikelyInlineMath("10") === false);
check("$10.50", () => isLikelyInlineMath("10.50") === false);
check("$100%", () => isLikelyInlineMath("100%") === false);
check("URL", () => isLikelyInlineMath("https://example.com") === false);
check("prose text", () => isLikelyInlineMath("hello world today") === false);
check("prose $x y z$ (spaces)", () => isLikelyInlineMath("x y z") === false);

// ── looksLikeFormula (CodeBlockOrMath fenced-code classifier) ──────────────────
// Mirrors the looksLikeFormula logic from CodeBlockOrMath.tsx.

const LATEX_COMMAND_RE = /\\(?:frac|sqrt|begin|end|sum|int|prod|lim|infty|partial|nabla|alpha|beta|gamma|delta|epsilon|theta|lambda|mu|pi|sigma|tau|phi|omega|mathbf|mathrm|mathcal|mathbb|mathfrak|text|textbf|textit|widehat|widetilde|overline|underline|vec|hat|tilde|bar|dot)(?![A-Za-z])/;

const looksLikeFormula = (content: string): boolean => {
  const trimmed = content.trim();
  if (!trimmed) return false;
  if (!trimmed.includes("\n") && LATEX_COMMAND_RE.test(trimmed)) return true;
  if (/\\begin\{/.test(trimmed) || /\\end\{/.test(trimmed)) return true;
  if (/\\(?:align|equation|gather|multline|matrix|pmatrix|bmatrix|array|tabular|split|cases)\b/.test(trimmed)) return true;
  if (trimmed.includes("\n")) return false;
  return false;
};

console.log("\nlooksLikeFormula — formula");
check("single-line \\frac", () => looksLikeFormula("\\frac{a}{b}") === true);
check("single-line \\sqrt", () => looksLikeFormula("\\sqrt{x^2+1}") === true);
check("single-line \\sum", () => looksLikeFormula("\\sum_{i=1}^{n} i") === true);
check("single-line \\int", () => looksLikeFormula("\\int_0^\\infty e^{-x} dx") === true);
check("multiline \\begin{aligned}", () => looksLikeFormula("\\begin{aligned}\nx &= 1 \\\\\ny &= 2\n\\end{aligned}") === true);
check("multiline \\begin{bmatrix}", () => looksLikeFormula("\\begin{bmatrix} a & b \\\\ c & d \\end{bmatrix}") === true);
check("multiline \\end only", () => looksLikeFormula("x = 1 \\\\\n\\end{aligned}") === true);
check("multiline \\equation", () => looksLikeFormula("\\equation\nE = mc^2\n") === true);

console.log("\nlooksLikeFormula — code");
check("multiline no-LaTeX", () => looksLikeFormula("line one\nline two\nline three") === false);
check("empty", () => looksLikeFormula("") === false);
check("plain text", () => looksLikeFormula("hello world") === false);
check("code snippet", () => looksLikeFormula("const x = 1;\nconst y = 2;") === false);

// ── Summary ────────────────────────────────────────────────────────────────────

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
