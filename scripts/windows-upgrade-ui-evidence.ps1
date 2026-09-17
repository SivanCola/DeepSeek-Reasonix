# The selector is deliberately scoped to the rendered transcript. Sidebar topic
# titles and offscreen accessibility nodes are not evidence of restored history.
function Get-UpgradeUIDescendants($element) {
  return $element.FindAll([Windows.Automation.TreeScope]::Descendants, [Windows.Automation.Condition]::TrueCondition)
}

function Test-VisibleUpgradeHistory($root, [string]$text) {
  if ([string]::IsNullOrWhiteSpace($text)) { return $false }
  foreach ($transcript in (Get-UpgradeUIDescendants $root)) {
    if (-not ([string]$transcript.Current.AutomationId).StartsWith('reasonix-chat-transcript-') -or $transcript.Current.IsOffscreen) { continue }
    foreach ($element in (Get-UpgradeUIDescendants $transcript)) {
      $current = $element.Current
      if (-not $current.IsOffscreen -and $current.BoundingRectangle.Width -gt 0 -and $current.BoundingRectangle.Height -gt 0 -and
          $current.Name.Contains($text)) { return $true }
    }
  }
  return $false
}
