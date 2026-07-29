package com.epam.reportportal.marketplace.auth;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertNull;

import com.epam.reportportal.marketplace.config.MarketplaceProperties;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

class SessionJwtServiceTest {

  private SessionJwtService sessionJwtService;

  @BeforeEach
  void setUp() {
    MarketplaceProperties properties = new MarketplaceProperties();
    properties.getAuth().getJwt().setSecret("unit-test-secret-key-minimum-32-chars");
    properties.getAuth().getJwt().setIssuer("marketplace.reportportal.io");
    properties.getAuth().getJwt().setTtlSeconds(3600);
    sessionJwtService = new SessionJwtService(properties);
  }

  @Test
  void createsAndValidatesSessionToken() {
    String token = sessionJwtService.createToken("operator@test");
    SessionJwtService.SessionPrincipal principal = sessionJwtService.validateToken(token);
    assertNotNull(principal);
    assertEquals("operator@test", principal.subject());
    assertNull(sessionJwtService.validateToken(token + "invalid"));
  }
}
