package com.epam.reportportal.marketplace.auth;

import jakarta.servlet.http.Cookie;
import jakarta.servlet.http.HttpServletRequest;
import jakarta.servlet.http.HttpServletResponse;

/**
 * Browser-bound carrier for the GitHub OAuth CSRF {@code state} value. The same signed state is
 * sent to GitHub in the authorize redirect and stored here so the callback can prove the browser
 * that started the flow is the one finishing it — without any server-side state map.
 */
public final class OAuthStateCookie {

  public static final String NAME = "mp_oauth_state";

  private OAuthStateCookie() {}

  public static void set(
      HttpServletRequest request, HttpServletResponse response, String state, long ttlSeconds) {
    Cookie cookie = base(state, (int) Math.min(Integer.MAX_VALUE, Math.max(1, ttlSeconds)));
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
    // Scoped to the OAuth endpoints so the state is not sent on every operator API call.
    cookie.setPath("/api/v1/auth/github");
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
