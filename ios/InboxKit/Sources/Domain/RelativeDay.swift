import Foundation

/// "last seen yesterday, 6:40 PM" style text: today shows the time, yesterday and the past week a day name, older an "Oct 5" date
/// (no year within the current year). Used by Alfather status and Business detail.
public enum RelativeDay {
    public static func string(_ date: Date, now: Date = AppClock.now, calendar: Calendar = .current, locale: Locale = .current) -> String {
        let time = timeText(date, calendar: calendar, locale: locale)
        if calendar.isDate(date, inSameDayAs: now) { return "today, \(time)" }
        if let y = calendar.date(byAdding: .day, value: -1, to: now), calendar.isDate(date, inSameDayAs: y) { return "yesterday, \(time)" }
        if let n = calendar.dateComponents([.day], from: calendar.startOfDay(for: date), to: calendar.startOfDay(for: now)).day, n >= 0, n < 7 {
            let f = DateFormatter(); f.calendar = calendar; f.locale = locale; f.timeZone = calendar.timeZone; f.setLocalizedDateFormatFromTemplate("EEEE")
            return "\(f.string(from: date)), \(time)"
        }
        let f = DateFormatter(); f.calendar = calendar; f.locale = locale; f.timeZone = calendar.timeZone
        f.setLocalizedDateFormatFromTemplate(calendar.isDate(date, equalTo: now, toGranularity: .year) ? "MMMd" : "MMMdy")
        return "\(f.string(from: date)), \(time)"
    }

    private static func timeText(_ date: Date, calendar: Calendar, locale: Locale) -> String {
        let f = DateFormatter(); f.calendar = calendar; f.locale = locale; f.timeZone = calendar.timeZone; f.setLocalizedDateFormatFromTemplate("jmm")
        return f.string(from: date)
    }
}
