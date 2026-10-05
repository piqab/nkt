import SwiftUI
import BackgroundTasks
import UserNotifications
import Shared

/// Launch: start the shared Koin graph, register the background refresh
/// that checks the hub's alerts, and route notification taps to the host.
final class AppDelegate: NSObject, UIApplicationDelegate, UNUserNotificationCenterDelegate {
    func application(
        _ application: UIApplication,
        didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]? = nil
    ) -> Bool {
        IosAppKt.startApp()
        UNUserNotificationCenter.current().delegate = self
        BGTaskScheduler.shared.register(forTaskWithIdentifier: IosAppKt.EVENTS_TASK_ID, using: nil) { task in
            EventChecks.shared.run {
                task.setTaskCompleted(success: true)
            }
        }
        return true
    }

    /// A tapped alert notification opens the host at the event's section.
    func userNotificationCenter(
        _ center: UNUserNotificationCenter,
        didReceive response: UNNotificationResponse,
        withCompletionHandler completionHandler: @escaping () -> Void
    ) {
        let info = response.notification.request.content.userInfo
        if let hostId = info["host_id"] as? Int64, let path = info["path"] as? String {
            IosAppKt.openHost(hostId: hostId, path: path)
        } else if let hostId = info["host_id"] as? NSNumber, let path = info["path"] as? String {
            IosAppKt.openHost(hostId: hostId.int64Value, path: path)
        }
        completionHandler()
    }

    func userNotificationCenter(
        _ center: UNUserNotificationCenter,
        willPresent notification: UNNotification,
        withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void
    ) {
        completionHandler([.banner, .sound])
    }
}

@main
struct iOSApp: App {
    @UIApplicationDelegateAdaptor(AppDelegate.self) var delegate

    var body: some Scene {
        WindowGroup {
            ComposeView().ignoresSafeArea(.keyboard)
        }
    }
}

/// The shared Compose UI (shared/ui's NktApp).
struct ComposeView: UIViewControllerRepresentable {
    func makeUIViewController(context: Context) -> UIViewController {
        IosAppKt.MainViewController()
    }

    func updateUIViewController(_ uiViewController: UIViewController, context: Context) {}
}
