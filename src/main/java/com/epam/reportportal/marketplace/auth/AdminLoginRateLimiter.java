package com.epam.reportportal.marketplace.auth;

import com.epam.reportportal.marketplace.config.MarketplaceProperties;
import com.epam.reportportal.marketplace.web.error.TooManyRequestsException;
import java.time.Clock;
import java.time.Instant;
import java.util.Iterator;
import java.util.Map;
import java.util.concurrent.ConcurrentHashMap;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.stereotype.Service;

/**
 * Per-username lockout for admin password login. IP throttling is expected at the edge (Cloud
 * Armor); this layer stops password spraying against a single account across replicas that share
 * sticky routing or a single pod.
 */
@Service
public class AdminLoginRateLimiter {

  private static final Logger LOGGER = LoggerFactory.getLogger(AdminLoginRateLimiter.class);
  private static final int MAX_TRACKED_USERNAMES = 10_000;

  private final MarketplaceProperties properties;
  private Clock clock = Clock.systemUTC();
  private final ConcurrentHashMap<String, AttemptState> attempts = new ConcurrentHashMap<>();

  public AdminLoginRateLimiter(MarketplaceProperties properties) {
    this.properties = properties;
  }

  /** Test seam for deterministic lockout timing. */
  AdminLoginRateLimiter withClock(Clock clock) {
    this.clock = clock;
    return this;
  }

  public void checkAllowed(String username) {
    if (!enabled()) {
      return;
    }
    String key = normalize(username);
    AttemptState state = attempts.get(key);
    if (state == null) {
      return;
    }
    Instant now = clock.instant();
    if (state.lockedUntil != null && now.isBefore(state.lockedUntil)) {
      LOGGER.warn(
          "Admin login rejected: username={} reason=locked lockedUntil={}",
          maskUsername(key),
          state.lockedUntil);
      throw new TooManyRequestsException("Too many login attempts");
    }
  }

  public void recordFailure(String username) {
    if (!enabled()) {
      return;
    }
    String key = normalize(username);
    Instant now = clock.instant();
    pruneIfNeeded(now);
    AttemptState updated = attempts.compute(key, (ignored, existing) -> {
      AttemptState state = existing == null ? new AttemptState() : existing;
      if (state.windowStarted == null
          || now.isAfter(state.windowStarted.plusSeconds(windowSeconds()))) {
        state.windowStarted = now;
        state.failuresInWindow = 0;
      }
      state.failuresInWindow++;
      state.totalFailures++;
      if (state.failuresInWindow >= maxAttempts()) {
        long lockSeconds = lockoutSeconds(state.totalFailures);
        state.lockedUntil = now.plusSeconds(lockSeconds);
      }
      return state;
    });
    LOGGER.warn(
        "Admin login failed: username={} failuresInWindow={} lockedUntil={}",
        maskUsername(key),
        updated.failuresInWindow,
        updated.lockedUntil);
  }

  public void recordSuccess(String username) {
    if (!enabled()) {
      return;
    }
    String key = normalize(username);
    attempts.remove(key);
    LOGGER.info("Admin login succeeded: username={}", maskUsername(key));
  }

  private boolean enabled() {
    return properties.getAuth().getLoginRateLimit().isEnabled();
  }

  private int maxAttempts() {
    return Math.max(1, properties.getAuth().getLoginRateLimit().getMaxAttempts());
  }

  private long windowSeconds() {
    return Math.max(1, properties.getAuth().getLoginRateLimit().getWindowSeconds());
  }

  private long lockoutSeconds(int totalFailures) {
    var cfg = properties.getAuth().getLoginRateLimit();
    long base = Math.max(1, cfg.getLockoutSeconds());
    long max = Math.max(base, cfg.getMaxLockoutSeconds());
    int over = Math.max(0, totalFailures - maxAttempts());
    double multiplier = Math.max(1.0, cfg.getBackoffMultiplier());
    long computed = (long) (base * Math.pow(multiplier, over));
    return Math.min(max, computed);
  }

  private void pruneIfNeeded(Instant now) {
    if (attempts.size() < MAX_TRACKED_USERNAMES) {
      return;
    }
    Iterator<Map.Entry<String, AttemptState>> it = attempts.entrySet().iterator();
    while (it.hasNext()) {
      AttemptState state = it.next().getValue();
      boolean windowExpired = state.windowStarted != null
          && now.isAfter(state.windowStarted.plusSeconds(windowSeconds()));
      boolean unlockExpired = state.lockedUntil == null || !now.isBefore(state.lockedUntil);
      if (windowExpired && unlockExpired) {
        it.remove();
      }
    }
  }

  private static String normalize(String username) {
    return username == null ? "" : username.trim().toLowerCase();
  }

  private static String maskUsername(String username) {
    if (username == null || username.isBlank()) {
      return "(blank)";
    }
    if (username.length() <= 2) {
      return "**";
    }
    return username.charAt(0) + "***" + username.charAt(username.length() - 1);
  }

  private static final class AttemptState {
    Instant windowStarted;
    int failuresInWindow;
    int totalFailures;
    Instant lockedUntil;
  }
}
