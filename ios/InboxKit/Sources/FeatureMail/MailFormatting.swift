import Foundation
import DesignSystem
import Domain
import Persistence

/// Row timestamps: time today, "Yesterday", weekday within a week, else "Oct 4".
public enum MailTime {
    public static func string(_ date: Date, now: Date = AppClock.now, calendar: Calendar = .current, locale: Locale = .current) -> String {
        let template: String
        if calendar.isDate(date, inSameDayAs: now) {
            template = "jmm"
        } else if let y = calendar.date(byAdding: .day, value: -1, to: now), calendar.isDate(date, inSameDayAs: y) {
            return "Yesterday"
        } else if let days = calendar.dateComponents([.day], from: calendar.startOfDay(for: date), to: calendar.startOfDay(for: now)).day, days < 7 {
            template = "EEE"
        } else {
            template = "MMMd"
        }
        return formatter(template, calendar, locale).string(from: date)
    }

    // A DateFormatter costs far more than formatting with it; rows are formatted on every list update.
    private static let lock = NSLock()
    nonisolated(unsafe) private static var formatters: [String: DateFormatter] = [:]
    private static func formatter(_ template: String, _ calendar: Calendar, _ locale: Locale) -> DateFormatter {
        let key = "\(template)|\(locale.identifier)|\(calendar.identifier)|\(calendar.timeZone.identifier)"
        lock.lock(); defer { lock.unlock() }
        if let f = formatters[key] { return f }
        let f = DateFormatter()
        f.calendar = calendar; f.locale = locale; f.timeZone = calendar.timeZone
        f.setLocalizedDateFormatFromTemplate(template)
        formatters[key] = f
        return f
    }
}

extension MailRowModel {
    init(_ m: MessageRecord, now: Date = AppClock.now) {
        let sender = [m.fromName, m.fromAddr].compactMap { $0 }.first { !$0.isEmpty } ?? "Unknown sender"
        self.init(id: m.id.uuidString, sender: sender, subject: m.subject?.isEmpty == false ? m.subject! : "(no subject)",
                  preview: m.snippet ?? "", time: MailTime.string(m.receivedAt, now: now), isUnread: !m.isRead)
    }
}
