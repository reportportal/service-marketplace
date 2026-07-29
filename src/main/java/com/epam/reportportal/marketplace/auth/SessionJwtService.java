package com.epam.reportportal.marketplace.auth;

import com.epam.reportportal.marketplace.config.MarketplaceProperties;
import com.nimbusds.jose.JOSEException;
import com.nimbusds.jose.JWSAlgorithm;
import com.nimbusds.jose.JWSHeader;
import com.nimbusds.jose.crypto.MACSigner;
import com.nimbusds.jose.crypto.MACVerifier;
import com.nimbusds.jwt.JWTClaimsSet;
import com.nimbusds.jwt.SignedJWT;
import java.nio.charset.StandardCharsets;
import java.time.Instant;
import java.util.Date;
import java.util.List;
import org.springframework.stereotype.Service;

@Service
public class SessionJwtService {

  public static final String ROLE_OPERATOR = "OPERATOR";

  private final MarketplaceProperties properties;

  public SessionJwtService(MarketplaceProperties properties) {
    this.properties = properties;
  }

  public String createToken(String subject) {
    try {
      Instant now = Instant.now();
      long ttl = properties.getAuth().getJwt().getTtlSeconds();
      JWTClaimsSet claims = new JWTClaimsSet.Builder()
          .subject(subject)
          .issuer(properties.getAuth().getJwt().getIssuer())
          .issueTime(Date.from(now))
          .expirationTime(Date.from(now.plusSeconds(ttl)))
          .claim("roles", List.of(ROLE_OPERATOR))
          .build();
      SignedJWT jwt = new SignedJWT(
          new JWSHeader(JWSAlgorithm.HS256), claims);
      jwt.sign(new MACSigner(secretBytes()));
      return jwt.serialize();
    } catch (JOSEException e) {
      throw new IllegalStateException("Failed to create session JWT", e);
    }
  }

  public SessionPrincipal validateToken(String token) {
    try {
      SignedJWT jwt = SignedJWT.parse(token);
      if (!jwt.verify(new MACVerifier(secretBytes()))) {
        return null;
      }
      JWTClaimsSet claims = jwt.getJWTClaimsSet();
      if (!properties.getAuth().getJwt().getIssuer().equals(claims.getIssuer())) {
        return null;
      }
      Date exp = claims.getExpirationTime();
      if (exp == null || exp.toInstant().isBefore(Instant.now())) {
        return null;
      }
      @SuppressWarnings("unchecked")
      List<String> roles = (List<String>) claims.getClaim("roles");
      return new SessionPrincipal(claims.getSubject(), roles != null ? roles : List.of());
    } catch (Exception e) {
      return null;
    }
  }

  public long getTtlSeconds() {
    return properties.getAuth().getJwt().getTtlSeconds();
  }

  private byte[] secretBytes() {
    String secret = properties.getAuth().getJwt().getSecret();
    if (secret == null || secret.length() < 32) {
      throw new IllegalStateException("JWT secret must be at least 32 characters");
    }
    return secret.getBytes(StandardCharsets.UTF_8);
  }

  public record SessionPrincipal(String subject, List<String> roles) {
    public boolean isOperator() {
      return roles.contains(ROLE_OPERATOR);
    }
  }
}
