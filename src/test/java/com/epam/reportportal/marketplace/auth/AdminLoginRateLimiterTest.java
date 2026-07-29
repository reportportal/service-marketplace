package com.epam.reportportal.marketplace.auth;

import static org.junit.jupiter.api.Assertions.assertDoesNotThrow;
import static org.junit.jupiter.api.Assertions.assertThrows;

import com.epam.reportportal.marketplace.config.MarketplaceProperties;
import com.epam.reportportal.marketplace.web.error.TooManyRequestsException;
import java.time.Clock;
import java.time.Instant;
import java.time.ZoneOffset;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

class AdminLoginRateLimiterTest {

  private MarketplaceProperties properties;
  private MutableClock clock;
  private AdminLoginRateLimiter limiter;

  @BeforeEach
  void setUp() {
    properties = new MarketplaceProperties();
    properties.getAuth().getLoginRateLimit().setEnabled(true);
    properties.getAuth().getLoginRateLimit().setMaxAttempts(3);
    properties.getAuth().getLoginRateLimit().setWindowSeconds(300);
    properties.getAuth().getLoginRateLimit().setLockoutSeconds(60);
    properties.getAuth().getLoginRateLimit().setBackoffMultiplier(2.0);
    properties.getAuth().getLoginRateLimit().setMaxLockoutSeconds(900);
    clock = new MutableClock(Instant.parse("2026-01-01T00:00:00Z"));
    limiter = new AdminLoginRateLimiter(properties).withClock(clock);
  }

  @Test
  void locksAfterMaxFailedAttempts() {
    limiter.recordFailure("admin");
    limiter.recordFailure("admin");
    limiter.recordFailure("admin");

    assertThrows(TooManyRequestsException.class, () -> limiter.checkAllowed("admin"));
  }

  @Test
  void allowsLoginAgainAfterLockoutExpires() {
    limiter.recordFailure("admin");
    limiter.recordFailure("admin");
    limiter.recordFailure("admin");
    clock.advanceSeconds(61);

    assertDoesNotThrow(() -> limiter.checkAllowed("admin"));
  }

  @Test
  void successClearsFailureCounters() {
    limiter.recordFailure("admin");
    limiter.recordFailure("admin");
    limiter.recordSuccess("admin");
    limiter.recordFailure("admin");
    limiter.recordFailure("admin");

    assertDoesNotThrow(() -> limiter.checkAllowed("admin"));
  }

  @Test
  void lockoutEscalatesWithBackoff() {
    for (int i = 0; i < 3; i++) {
      limiter.recordFailure("admin");
    }
    assertThrows(TooManyRequestsException.class, () -> limiter.checkAllowed("admin"));
    clock.advanceSeconds(61);
    limiter.recordFailure("admin");
    // second lockout: base * 2^(4-3) = 120s
    clock.advanceSeconds(61);
    assertThrows(TooManyRequestsException.class, () -> limiter.checkAllowed("admin"));
    clock.advanceSeconds(60);
    assertDoesNotThrow(() -> limiter.checkAllowed("admin"));
  }

  private static final class MutableClock extends Clock {
    private Instant instant;

    MutableClock(Instant instant) {
      this.instant = instant;
    }

    void advanceSeconds(long seconds) {
      instant = instant.plusSeconds(seconds);
    }

    @Override
    public ZoneOffset getZone() {
      return ZoneOffset.UTC;
    }

    @Override
    public Clock withZone(java.time.ZoneId zone) {
      return this;
    }

    @Override
    public Instant instant() {
      return instant;
    }
  }
}
