package com.epam.reportportal.marketplace.auth;

import com.epam.reportportal.marketplace.config.MarketplaceProperties;
import com.epam.reportportal.marketplace.web.error.ForbiddenException;
import com.epam.reportportal.marketplace.web.error.UnauthorizedException;
import com.nimbusds.jose.JWSAlgorithm;
import com.nimbusds.jose.jwk.source.JWKSource;
import com.nimbusds.jose.jwk.source.JWKSourceBuilder;
import com.nimbusds.jose.proc.JWSKeySelector;
import com.nimbusds.jose.proc.JWSVerificationKeySelector;
import com.nimbusds.jose.proc.SecurityContext;
import com.nimbusds.jwt.JWTClaimsSet;
import com.nimbusds.jwt.proc.ConfigurableJWTProcessor;
import com.nimbusds.jwt.proc.DefaultJWTProcessor;
import java.net.URI;
import java.net.URL;
import java.time.Duration;
import java.time.Instant;
import java.util.Map;
import org.springframework.stereotype.Service;

@Service
public class OidcPublishAuthService {

  private static final Duration JWKS_TTL = Duration.ofHours(24);
  private static final Duration JWKS_REFRESH_TIMEOUT = Duration.ofSeconds(30);

  private final MarketplaceProperties properties;
  private volatile CachedJwks cachedJwks;

  public OidcPublishAuthService(MarketplaceProperties properties) {
    this.properties = properties;
  }

  public void validatePublishToken(String token, String pluginId) {
    try {
      JWTClaimsSet claims = parseAndVerify(token);
      applyClaims(claims, pluginId);
    } catch (UnauthorizedException | ForbiddenException e) {
      throw e;
    } catch (Exception first) {
      // ADR-014: refresh JWKS on signature failure, then retry once
      invalidateJwksCache();
      try {
        JWTClaimsSet claims = parseAndVerify(token);
        applyClaims(claims, pluginId);
      } catch (UnauthorizedException | ForbiddenException e) {
        throw e;
      } catch (Exception e) {
        throw new UnauthorizedException("Invalid OIDC token");
      }
    }
  }

  private void applyClaims(JWTClaimsSet claims, String pluginId) throws Exception {
    requireExpectedIssuer(claims.getIssuer());
    Object aud = claims.getAudience();
    String audience = properties.getPublishOidcTrust().getAudience();
    boolean audOk = aud instanceof String s && s.equals(audience)
        || aud instanceof java.util.List<?> list && list.contains(audience);
    if (!audOk) {
      throw new UnauthorizedException("Invalid OIDC audience");
    }
    authorizeSource(claims.getStringClaim("repository"), pluginId);
  }

  void authorizeSource(String repository, String pluginId) {
    Map<String, String> allowedSources = properties.getPublishOidcTrust().getAllowedSources();
    if (allowedSources == null || allowedSources.isEmpty()) {
      throw new ForbiddenException("OIDC publishing is not configured");
    }
    if (pluginId == null || pluginId.isBlank()) {
      return;
    }
    String expectedPluginId = allowedSources.get(repository);
    if (expectedPluginId == null || !expectedPluginId.equals(pluginId)) {
      throw new ForbiddenException("Repository not authorized to publish plugin " + pluginId);
    }
  }

  void requireExpectedIssuer(String issuer) {
    if (!matchesConfiguredIssuer(issuer)) {
      throw new UnauthorizedException("Invalid OIDC issuer");
    }
  }

  boolean matchesConfiguredIssuer(String issuer) {
    String expected = properties.getPublishOidcTrust().getIssuer();
    return expected != null && expected.equals(issuer);
  }

  private void invalidateJwksCache() {
    synchronized (this) {
      cachedJwks = null;
    }
  }

  public boolean isOidcToken(String token) {
    try {
      JWTClaimsSet claims = com.nimbusds.jwt.SignedJWT.parse(token).getJWTClaimsSet();
      return matchesConfiguredIssuer(claims.getIssuer());
    } catch (Exception e) {
      return false;
    }
  }

  private JWTClaimsSet parseAndVerify(String token) throws Exception {
    ConfigurableJWTProcessor<SecurityContext> processor = new DefaultJWTProcessor<>();
    processor.setJWSKeySelector(keySelector());
    return processor.process(token, null);
  }

  private JWSKeySelector<SecurityContext> keySelector() throws Exception {
    return new JWSVerificationKeySelector<>(JWSAlgorithm.RS256, jwkSource());
  }

  private JWKSource<SecurityContext> jwkSource() throws Exception {
    CachedJwks current = cachedJwks;
    if (current == null || current.expiresAt.isBefore(Instant.now())) {
      synchronized (this) {
        current = cachedJwks;
        if (current == null || current.expiresAt.isBefore(Instant.now())) {
          URL jwksUrl = URI.create(properties.getPublishOidcTrust().getIssuer()
              + "/.well-known/jwks.json").toURL();
          JWKSource<SecurityContext> source = JWKSourceBuilder.<SecurityContext>create(jwksUrl)
              .cache(JWKS_TTL.toMillis(), JWKS_REFRESH_TIMEOUT.toMillis())
              .build();
          cachedJwks = new CachedJwks(source, Instant.now().plus(JWKS_TTL));
          current = cachedJwks;
        }
      }
    }
    return current.source;
  }

  private record CachedJwks(JWKSource<SecurityContext> source, Instant expiresAt) {}
}
