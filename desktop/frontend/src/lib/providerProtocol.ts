// Presentation and conservative endpoint checks shared by provider settings.
export function providerProtocolLabel(kind: string): string {
  switch (kind.trim().toLowerCase()) {
    case "anthropic": return "Anthropic Messages (/v1/messages)";
    case "openai": return "Chat Completions (/chat/completions)";
    case "responses": return "Responses (/responses)";
    case "dashscope-responses": return "百炼 Responses (/responses)";
    default: return kind;
  }
}

export function providerEndpointMismatch(kind: string, address: string): boolean {
  let path: string;
  try { path = new URL(address).pathname.replace(/\/+$/, ""); } catch { return false; }
  const protocol = kind.trim().toLowerCase();
  const expected = protocol === "anthropic" ? "/messages"
    : protocol === "openai" ? "/chat/completions"
    : protocol === "responses" || protocol === "dashscope-responses" ? "/responses" : "";
  // Base URLs and custom gateway paths cannot be inferred safely.
  return Boolean(expected) && ["/messages", "/chat/completions", "/responses"].some(suffix => path.endsWith(suffix)) && !path.endsWith(expected);
}
