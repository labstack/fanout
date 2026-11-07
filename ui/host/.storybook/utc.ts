import { spyOn } from "storybook/test";

/** Browser previews have no process TZ. Pin the local Date APIs as well as
 * Intl defaults so axes, tooltips, and time controls share the story's UTC. */
export function pinUTC() {
  const RealDateTimeFormat = Intl.DateTimeFormat;
  Intl.DateTimeFormat = new Proxy(RealDateTimeFormat, {
    construct: (target, [locales, options]) => new target(locales, { timeZone: "UTC", ...options }),
    apply: (target, _receiver, [locales, options]) => target(locales, { timeZone: "UTC", ...options }),
  });
  for (const [local, utc] of [
    ["getFullYear", "getUTCFullYear"], ["getMonth", "getUTCMonth"],
    ["getDate", "getUTCDate"], ["getDay", "getUTCDay"],
    ["getHours", "getUTCHours"], ["getMinutes", "getUTCMinutes"],
    ["getSeconds", "getUTCSeconds"], ["getMilliseconds", "getUTCMilliseconds"],
  ] as const) spyOn(Date.prototype, local).mockImplementation(Date.prototype[utc]);
  spyOn(Date.prototype, "setFullYear").mockImplementation(Date.prototype.setUTCFullYear);
  spyOn(Date.prototype, "setMonth").mockImplementation(Date.prototype.setUTCMonth);
  spyOn(Date.prototype, "setDate").mockImplementation(Date.prototype.setUTCDate);
  spyOn(Date.prototype, "setHours").mockImplementation(Date.prototype.setUTCHours);
  spyOn(Date.prototype, "setMinutes").mockImplementation(Date.prototype.setUTCMinutes);
  spyOn(Date.prototype, "setSeconds").mockImplementation(Date.prototype.setUTCSeconds);
  spyOn(Date.prototype, "setMilliseconds").mockImplementation(Date.prototype.setUTCMilliseconds);
  spyOn(Date.prototype, "getTimezoneOffset").mockReturnValue(0);
  for (const method of ["toLocaleString", "toLocaleDateString", "toLocaleTimeString"] as const) {
    const original = Date.prototype[method];
    spyOn(Date.prototype, method).mockImplementation(function (this: Date, locales?: Intl.LocalesArgument, options?: Intl.DateTimeFormatOptions) {
      return original.call(this, locales, { timeZone: "UTC", ...options });
    });
  }
  return () => { Intl.DateTimeFormat = RealDateTimeFormat; };
}
