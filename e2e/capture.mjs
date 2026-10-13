export function isLocalOrigin(url = process.env.E2E_BASE_URL || "https://localhost") {
  return /^https?:\/\/(localhost|127\.0\.0\.1)(:|\/|$)/.test(url);
}

export function captureSettings(baseURL = process.env.E2E_BASE_URL || "https://localhost") {
  if (isLocalOrigin(baseURL)) {
    return { trace: "retain-on-failure", screenshot: "off", video: "off" };
  }
  return { trace: "off", screenshot: "off", video: "off" };
}
