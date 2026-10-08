// noVNC поставляется без типов — описано то, что используют VNCModal и
// ScreenPopout.
declare module '@novnc/novnc' {
  export default class RFB extends EventTarget {
    constructor(target: HTMLElement, url: string, options?: { credentials?: { password?: string }; wsProtocols?: string[] })
    scaleViewport: boolean
    resizeSession: boolean
    viewOnly: boolean
    focusOnClick: boolean
    disconnect(): void
    sendCredentials(credentials: { password?: string }): void
    sendCtrlAltDel(): void
    /** Нажатие (down не задан — нажатие и отпускание) клавиши по keysym X11. */
    sendKey(keysym: number, code: string | null, down?: boolean): void
    focus(): void
  }
}
