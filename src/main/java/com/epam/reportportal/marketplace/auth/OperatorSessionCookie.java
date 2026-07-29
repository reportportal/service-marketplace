package com.epam.reportportal.marketplace.auth;

import jakarta.servlet.http.Cookie;
import jakarta.servlet.http.HttpServletRequest;
import jakarta.servlet.http.HttpServletResponse;

/**
 * Browser session carrier for the operator JWT. The token must never appear in a redirect query
 * string (browser history, access logs, Referer); it travels as an HttpOnly cookie instead.
 */
public final class OperatorSessionCookie {

  public static final String NAME = "mp_operator_session";

  private OperatorSessionCookie() {}

  public static void set(
      HttpServletRequest request, HttpServletResponse response, String token, long ttlSeconds) {
    Cookie cookie = base(token, (int) Math.min(Integer.MAX_VALUE, Math.max(1, ttlSeconds)));
    cookie.setSecure(isSecure(request));
    response.addCookie(cookie);
  }

  public static void clear(HttpServletRequest request, HttpServletResponse response) {
    Cookie cookie = base("", 0);
    cookie.setSecure(isSecure(request));
    response.addCookie(cookie);
  }

  public static String read(HttpServletRequest request) {
    Cookie[] cookies = request.getCookies();
    if (cookies == null) {
      return null;
    }
    for (Cookie cookie : cookies) {
      if (NAME.equals(cookie.getName())) {
        String value = cookie.getValue();
        return value == null || value.isBlank() ? null : value;
      }
    }
    return null;
  }

  private static Cookie base(String value, int maxAgeSeconds) {
    Cookie cookie = new Cookie(NAME, value);
    cookie.setHttpOnly(true);
    cookie.setPath("/");
    cookie.setAttribute("SameSite", "Lax");
    cookie.setMaxAge(maxAgeSeconds);
    return cookie;
  }

  private static boolean isSecure(HttpServletRequest request) {
    if (request.isSecure()) {
      return true;
    }
    String forwarded = request.getHeader("X-Forwarded-Proto");
    return forwarded != null && forwarded.equalsIgnoreCase("https");
  }
}
