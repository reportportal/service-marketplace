package com.epam.reportportal.marketplace.auth;

import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

import com.epam.reportportal.marketplace.config.MarketplaceProperties;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

class AdminAuthServiceTest {

  private static final String ADMIN_HASH =
      "$2a$10$bbdLQFSI9d1QNOxWBerBceaVlOSl30P8PGK7i7bPG9bQ8jOVQIrja";

  private MarketplaceProperties properties;
  private AdminAuthService service;

  @BeforeEach
  void setUp() {
    properties = new MarketplaceProperties();
    properties.getAuth().getAdmin().setUsername("admin");
    properties.getAuth().getAdmin().setPasswordHash(ADMIN_HASH);
    service = new AdminAuthService(properties);
  }

  @Test
  void acceptsMatchingUsernameAndPasswordAgainstBcryptHash() {
    assertTrue(service.authenticate("admin", "admin"));
  }

  @Test
  void rejectsWrongPassword() {
    assertFalse(service.authenticate("admin", "wrong"));
  }

  @Test
  void rejectsWrongUsernameEvenWhenPasswordWouldMatch() {
    assertFalse(service.authenticate("other", "admin"));
  }

  @Test
  void rejectsWhenPasswordHashIsMissing() {
    properties.getAuth().getAdmin().setPasswordHash("");
    assertFalse(service.authenticate("admin", "admin"));
  }
}
