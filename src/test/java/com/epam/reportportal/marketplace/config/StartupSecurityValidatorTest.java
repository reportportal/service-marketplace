package com.epam.reportportal.marketplace.config;

import static org.junit.jupiter.api.Assertions.assertDoesNotThrow;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import org.junit.jupiter.api.Test;
import org.springframework.core.env.StandardEnvironment;

class StartupSecurityValidatorTest {

  private static final String SHIPPED_ADMIN_HASH =
      "$2a$10$bbdLQFSI9d1QNOxWBerBceaVlOSl30P8PGK7i7bPG9bQ8jOVQIrja";
  private static final String STRONG_SECRET = "1SBmEo0Ck0DDIkoUCLPuvBBBTFrqRVJ0";

  @Test
  void refusesToStartWithoutAJwtSecret() {
    MarketplaceProperties properties = new MarketplaceProperties();
    properties.getAuth().getAdmin().setPasswordHash("$2a$10$" + "b".repeat(53));

    IllegalStateException failure =
        assertThrows(IllegalStateException.class, () -> validate(properties, "helm"));
    assertTrue(failure.getMessage().contains("JWT_SECRET is not set"), failure.getMessage());
  }

  @Test
  void refusesToStartWithShippedCredentials() {
    MarketplaceProperties properties = new MarketplaceProperties();
    properties.getAuth().getJwt().setSecret("local-dev-jwt-secret-key-min-32-chars!!");
    properties.getAuth().getAdmin().setPasswordHash(SHIPPED_ADMIN_HASH);

    IllegalStateException failure =
        assertThrows(IllegalStateException.class, () -> validate(properties, "helm"));
    assertTrue(failure.getMessage().contains("shipped in this repository"), failure.getMessage());
    assertTrue(failure.getMessage().contains("ADMIN_PASSWORD_HASH"), failure.getMessage());
  }

  @Test
  void refusesToStartWithAShortJwtSecretOrMissingPasswordHash() {
    MarketplaceProperties properties = new MarketplaceProperties();
    properties.getAuth().getJwt().setSecret("too-short");

    IllegalStateException failure =
        assertThrows(IllegalStateException.class, () -> validate(properties, "helm"));
    assertTrue(failure.getMessage().contains("shorter than 32 characters"), failure.getMessage());
    assertTrue(failure.getMessage().contains("ADMIN_PASSWORD_HASH is not set"), failure.getMessage());
  }

  @Test
  void startsWithDeploymentProvidedCredentials() {
    MarketplaceProperties properties = new MarketplaceProperties();
    properties.getAuth().getJwt().setSecret(STRONG_SECRET);
    properties.getAuth().getAdmin().setPasswordHash("$2a$10$" + "b".repeat(53));

    assertDoesNotThrow(() -> validate(properties, "helm"));
  }

  @Test
  void allowsShippedCredentialsUnderDevelopmentProfiles() {
    MarketplaceProperties properties = new MarketplaceProperties();
    properties.getAuth().getJwt().setSecret("local-dev-jwt-secret-key-min-32-chars!!");
    properties.getAuth().getAdmin().setPasswordHash(SHIPPED_ADMIN_HASH);

    assertDoesNotThrow(() -> validate(properties, "local"));
    assertDoesNotThrow(() -> validate(properties, "test"));
  }

  private static void validate(MarketplaceProperties properties, String... activeProfiles) {
    StandardEnvironment environment = new StandardEnvironment();
    environment.setActiveProfiles(activeProfiles);
    new StartupSecurityValidator(properties, environment).validate();
  }
}
