// noVNC поставляется без типов — описано то, что использует VNCModal.
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
    focus(): void
  }
}
