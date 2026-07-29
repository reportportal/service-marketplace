package com.epam.reportportal.marketplace.config;

import jakarta.annotation.PostConstruct;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.List;
import java.util.Set;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.core.env.Environment;
import org.springframework.stereotype.Component;

/**
 * Refuses to start when a deployment still carries the credentials shipped in this repository.
 * Operator sessions are minted from these values, so a jar deployed without overrides would
 * otherwise grant full operator access to anyone who has read the source.
 */
@Component
public class StartupSecurityValidator {

  private static final Logger LOGGER = LoggerFactory.getLogger(StartupSecurityValidator.class);

  private static final int MIN_JWT_SECRET_LENGTH = 32;
  private static final Set<String> DEVELOPMENT_PROFILES = Set.of("local", "test");
  private static final Set<String> SHIPPED_JWT_SECRETS = Set.of(
      "change-me-in-production-use-long-random-string",
      "local-dev-jwt-secret-key-min-32-chars!!",
      "test-secret-key-at-least-32-characters-long");
  private static final String SHIPPED_ADMIN_PASSWORD_HASH =
      "$2a$10$bbdLQFSI9d1QNOxWBerBceaVlOSl30P8PGK7i7bPG9bQ8jOVQIrja";

  private final MarketplaceProperties properties;
  private final Environment environment;

  public StartupSecurityValidator(MarketplaceProperties properties, Environment environment) {
    this.properties = properties;
    this.environment = environment;
  }

  @PostConstruct
  void validate() {
    if (isDevelopmentProfile()) {
      LOGGER.warn("Development profile active — shipped credentials are in use. "
          + "Never expose this configuration outside a developer machine.");
      return;
    }
    List<String> problems = new ArrayList<>();
    checkJwtSecret(problems);
    checkAdminCredentials(problems);
    if (!problems.isEmpty()) {
      throw new IllegalStateException(
          "Refusing to start with insecure operator credentials: " + String.join("; ", problems)
              + ". Provide the missing values, or activate the 'local' profile for development.");
    }
  }

  private void checkJwtSecret(List<String> problems) {
    String secret = properties.getAuth().getJwt().getSecret();
    if (secret == null || secret.isBlank()) {
      problems.add("JWT_SECRET is not set");
    } else if (secret.length() < MIN_JWT_SECRET_LENGTH) {
      problems.add("JWT_SECRET is shorter than " + MIN_JWT_SECRET_LENGTH + " characters");
    } else if (SHIPPED_JWT_SECRETS.contains(secret)) {
      problems.add("JWT_SECRET is a value shipped in this repository");
    }
  }

  private void checkAdminCredentials(List<String> problems) {
    String hash = properties.getAuth().getAdmin().getPasswordHash();
    if (hash == null || hash.isBlank()) {
      problems.add("ADMIN_PASSWORD_HASH is not set");
    } else if (SHIPPED_ADMIN_PASSWORD_HASH.equals(hash)) {
      problems.add("ADMIN_PASSWORD_HASH is the development hash shipped in this repository");
    }
  }

  private boolean isDevelopmentProfile() {
    return Arrays.stream(environment.getActiveProfiles()).anyMatch(DEVELOPMENT_PROFILES::contains);
  }
}
