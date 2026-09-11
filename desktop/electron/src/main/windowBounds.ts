export interface WindowRect {
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface PersistedBoundsSource {
  isMaximized(): boolean;
  isMinimized(): boolean;
  isFullScreen(): boolean;
  getBounds(): WindowRect;
  getNormalBounds(): WindowRect;
}

// persistedWindowRect returns the rectangle worth persisting as the window's
// restore geometry. While the window is maximized, minimized, or fullscreen the
// live frame is not a valid restore rect — on Windows the maximized frame even
// overflows the work area — so the normal bounds are reported instead. Keeping
// the maximized frame as the saved restore rect resurrects as an oversized
// windowed window the next time the app leaves the maximized state.
export function persistedWindowRect(win: PersistedBoundsSource): WindowRect {
  if (win.isMaximized() || win.isMinimized() || win.isFullScreen()) {
    return win.getNormalBounds();
  }
  return win.getBounds();
}
