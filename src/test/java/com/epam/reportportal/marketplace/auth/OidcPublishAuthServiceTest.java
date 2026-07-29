package com.epam.reportportal.marketplace.auth;

import static org.junit.jupiter.api.Assertions.assertDoesNotThrow;
import static org.junit.jupiter.api.Assertions.assertThrows;

import com.epam.reportportal.marketplace.config.MarketplaceProperties;
import com.epam.reportportal.marketplace.web.error.ForbiddenException;
import com.epam.reportportal.marketplace.web.error.UnauthorizedException;
import java.util.Map;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.ValueSource;

class OidcPublishAuthServiceTest {

  @Test
  void rejectsOidcPublishingWhenAllowedSourcesIsEmpty() {
    OidcPublishAuthService service = serviceWithAllowedSources(Map.of());

    assertThrows(
        ForbiddenException.class,
        () -> service.authorizeSource("reportportal/plugin-jira", null));
    assertThrows(
        ForbiddenException.class,
        () -> service.authorizeSource("reportportal/plugin-jira", "plugin-jira"));
  }

  @Test
  void permitsConfiguredSourceToAuthenticateAndPublishItsPlugin() {
    OidcPublishAuthService service =
        serviceWithAllowedSources(Map.of("reportportal/plugin-jira", "plugin-jira"));

    assertDoesNotThrow(
        () -> service.authorizeSource("reportportal/plugin-jira", null));
    assertDoesNotThrow(
        () -> service.authorizeSource("reportportal/plugin-jira", "plugin-jira"));
  }

  @Test
  void rejectsUnknownSourceAndPluginMismatch() {
    OidcPublishAuthService service =
        serviceWithAllowedSources(Map.of("reportportal/plugin-jira", "plugin-jira"));

    assertThrows(
        ForbiddenException.class,
        () -> service.authorizeSource("attacker/plugin", "plugin-jira"));
    assertThrows(
        ForbiddenException.class,
        () -> service.authorizeSource("reportportal/plugin-jira", "plugin-slack"));
  }

  @Test
  void acceptsExactConfiguredOidcIssuer() {
    OidcPublishAuthService service = serviceWithAllowedSources(Map.of());

    assertDoesNotThrow(
        () -> service.requireExpectedIssuer("https://token.actions.githubusercontent.com"));
  }

  @ParameterizedTest
  @ValueSource(
      strings = {
        "https://evil.actions.githubusercontent.com.attacker.com",
        "https://notgithub.com/actions.githubusercontent.com",
        "http://token.actions.githubusercontent.com",
        "https://token.actions.githubusercontent.com/",
        ""
      })
  void rejectsSubstringIssuerBypassAttempts(String issuer) {
    OidcPublishAuthService service = serviceWithAllowedSources(Map.of());

    assertThrows(UnauthorizedException.class, () -> service.requireExpectedIssuer(issuer));
  }

  @Test
  void matchesConfiguredIssuerExactly() {
    OidcPublishAuthService service = serviceWithAllowedSources(Map.of());

    org.junit.jupiter.api.Assertions.assertTrue(
        service.matchesConfiguredIssuer("https://token.actions.githubusercontent.com"));
    org.junit.jupiter.api.Assertions.assertFalse(
        service.matchesConfiguredIssuer("https://evil.actions.githubusercontent.com.attacker.com"));
  }

  private static OidcPublishAuthService serviceWithAllowedSources(
      Map<String, String> allowedSources) {
    MarketplaceProperties properties = new MarketplaceProperties();
    properties.getPublishOidcTrust().setAllowedSources(allowedSources);
    return new OidcPublishAuthService(properties);
  }
}
