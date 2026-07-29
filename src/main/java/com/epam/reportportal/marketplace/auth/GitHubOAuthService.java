package com.epam.reportportal.marketplace.auth;

import com.epam.reportportal.marketplace.config.MarketplaceProperties;
import com.epam.reportportal.marketplace.web.dto.AuthTokenResponseDto;
import com.epam.reportportal.marketplace.web.error.ForbiddenException;
import com.epam.reportportal.marketplace.web.error.ServiceUnavailableException;
import com.epam.reportportal.marketplace.web.error.UnauthorizedException;
import com.nimbusds.jose.JOSEException;
import com.nimbusds.jose.JWSAlgorithm;
import com.nimbusds.jose.JWSHeader;
import com.nimbusds.jose.crypto.MACSigner;
import com.nimbusds.jose.crypto.MACVerifier;
import com.nimbusds.jwt.JWTClaimsSet;
import com.nimbusds.jwt.SignedJWT;
import jakarta.servlet.http.HttpServletRequest;
import jakarta.servlet.http.HttpServletResponse;
import java.net.URI;
import java.net.URLEncoder;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.time.Instant;
import java.util.Date;
import java.util.Map;
import java.util.UUID;
import org.springframework.boot.restclient.RestTemplateBuilder;
import org.springframework.http.HttpEntity;
import org.springframework.http.HttpHeaders;
import org.springframework.http.HttpMethod;
import org.springframework.http.MediaType;
import org.springframework.http.ResponseEntity;
import org.springframework.stereotype.Service;
import org.springframework.util.LinkedMultiValueMap;
import org.springframework.util.MultiValueMap;
import org.springframework.web.client.RestTemplate;
import org.springframework.web.util.UriComponentsBuilder;

@Service
public class GitHubOAuthService {

  static final String STATE_PURPOSE = "oauth-state";
  static final long DEFAULT_STATE_TTL_SECONDS = 600;

  private final MarketplaceProperties properties;
  private final SessionJwtService sessionJwtService;
  /** Package-visible so unit tests can bind {@code MockRestServiceServer}. */
  final RestTemplate restTemplate;

  public GitHubOAuthService(
      MarketplaceProperties properties,
      SessionJwtService sessionJwtService,
      RestTemplateBuilder restTemplateBuilder) {
    this.properties = properties;
    this.sessionJwtService = sessionJwtService;
    this.restTemplate = restTemplateBuilder.build();
  }

  public URI buildLoginRedirect(HttpServletRequest request, HttpServletResponse response) {
    String clientId = requireConfigured();
    long ttl = stateTtlSeconds();
    String state = createStateToken(ttl);
    OAuthStateCookie.set(request, response, state, ttl);
    return UriComponentsBuilder.fromUriString("https://github.com/login/oauth/authorize")
        .queryParam("client_id", clientId)
        .queryParam("redirect_uri", properties.getAuth().getGithub().getRedirectUri())
        .queryParam("scope", "read:org")
        .queryParam("state", state)
        .build(true)
        .toUri();
  }

  public AuthTokenResponseDto handleCallback(
      String code,
      String state,
      HttpServletRequest request,
      HttpServletResponse response) {
    requireConfigured();
    String cookieState = OAuthStateCookie.read(request);
    OAuthStateCookie.clear(request, response);
    validateState(cookieState, state);
    String accessToken = exchangeCode(code);
    String login = fetchUserLogin(accessToken);
    verifyMembership(accessToken, login);
    return issueToken(login);
  }

  /**
   * GitHub login is unavailable rather than degraded when credentials are missing: an operator
   * session must always be backed by a verified GitHub identity, never by a local stand-in.
   */
  private String requireConfigured() {
    String clientId = properties.getAuth().getGithub().getClientId();
    String clientSecret = properties.getAuth().getGithub().getClientSecret();
    if (clientId == null || clientId.isBlank() || clientSecret == null || clientSecret.isBlank()) {
      throw new ServiceUnavailableException("GitHub OAuth is not configured");
    }
    return clientId;
  }

  private AuthTokenResponseDto issueToken(String subject) {
    String token = sessionJwtService.createToken(subject);
    return new AuthTokenResponseDto(token, "Bearer", sessionJwtService.getTtlSeconds());
  }

  String createStateToken(long ttlSeconds) {
    try {
      Instant now = Instant.now();
      JWTClaimsSet claims = new JWTClaimsSet.Builder()
          .jwtID(UUID.randomUUID().toString())
          .issuer(properties.getAuth().getJwt().getIssuer())
          .issueTime(Date.from(now))
          .expirationTime(Date.from(now.plusSeconds(ttlSeconds)))
          .claim("purpose", STATE_PURPOSE)
          .build();
      SignedJWT jwt = new SignedJWT(new JWSHeader(JWSAlgorithm.HS256), claims);
      jwt.sign(new MACSigner(secretBytes()));
      return jwt.serialize();
    } catch (JOSEException e) {
      throw new IllegalStateException("Failed to create OAuth state token", e);
    }
  }

  void validateState(String cookieState, String queryState) {
    if (cookieState == null
        || queryState == null
        || !constantTimeEquals(cookieState, queryState)
        || !isValidStateToken(queryState)) {
      throw new UnauthorizedException("Invalid OAuth state");
    }
  }

  private boolean isValidStateToken(String state) {
    try {
      SignedJWT jwt = SignedJWT.parse(state);
      if (!jwt.verify(new MACVerifier(secretBytes()))) {
        return false;
      }
      JWTClaimsSet claims = jwt.getJWTClaimsSet();
      if (!properties.getAuth().getJwt().getIssuer().equals(claims.getIssuer())) {
        return false;
      }
      if (!STATE_PURPOSE.equals(claims.getStringClaim("purpose"))) {
        return false;
      }
      Date exp = claims.getExpirationTime();
      return exp != null && !exp.toInstant().isBefore(Instant.now());
    } catch (Exception e) {
      return false;
    }
  }

  private static boolean constantTimeEquals(String left, String right) {
    byte[] a = left.getBytes(StandardCharsets.UTF_8);
    byte[] b = right.getBytes(StandardCharsets.UTF_8);
    return MessageDigest.isEqual(a, b);
  }

  private long stateTtlSeconds() {
    long configured = properties.getAuth().getGithub().getOauthStateTtlSeconds();
    return configured > 0 ? configured : DEFAULT_STATE_TTL_SECONDS;
  }

  private byte[] secretBytes() {
    String secret = properties.getAuth().getJwt().getSecret();
    if (secret == null || secret.length() < 32) {
      throw new IllegalStateException("JWT secret must be at least 32 characters");
    }
    return secret.getBytes(StandardCharsets.UTF_8);
  }

  private String exchangeCode(String code) {
    MultiValueMap<String, String> body = new LinkedMultiValueMap<>();
    body.add("client_id", properties.getAuth().getGithub().getClientId());
    body.add("client_secret", properties.getAuth().getGithub().getClientSecret());
    body.add("code", code);
    body.add("redirect_uri", properties.getAuth().getGithub().getRedirectUri());
    HttpHeaders headers = new HttpHeaders();
    headers.setContentType(MediaType.APPLICATION_FORM_URLENCODED);
    headers.setAccept(java.util.List.of(MediaType.APPLICATION_JSON));
    @SuppressWarnings("unchecked")
    Map<String, Object> response = restTemplate.postForObject(
        "https://github.com/login/oauth/access_token",
        new HttpEntity<>(body, headers),
        Map.class);
    if (response == null || response.get("access_token") == null) {
      throw new UnauthorizedException("GitHub token exchange failed");
    }
    return response.get("access_token").toString();
  }

  private String fetchUserLogin(String accessToken) {
    HttpHeaders headers = new HttpHeaders();
    headers.setBearerAuth(accessToken);
    headers.setAccept(java.util.List.of(MediaType.APPLICATION_JSON));
    @SuppressWarnings("unchecked")
    Map<String, Object> user = restTemplate.exchange(
        "https://api.github.com/user",
        HttpMethod.GET,
        new HttpEntity<>(headers),
        Map.class).getBody();
    if (user == null || user.get("login") == null) {
      throw new UnauthorizedException("Unable to fetch GitHub user");
    }
    return user.get("login").toString();
  }

  void verifyMembership(String accessToken, String login) {
    String org = properties.getAuth().getGithub().getAllowedOrg();
    String team = properties.getAuth().getGithub().getAllowedTeam();
    boolean teamConfigured = team != null && !team.isBlank();
    if (teamConfigured && (org == null || org.isBlank())) {
      throw new ForbiddenException("GitHub membership check failed");
    }
    if (org != null && !org.isBlank() && !isOrgMember(accessToken, org, login)) {
      throw new ForbiddenException("GitHub membership check failed");
    }
    if (teamConfigured && !isTeamMember(accessToken, org, team, login)) {
      throw new ForbiddenException("GitHub membership check failed");
    }
  }

  private boolean isOrgMember(String accessToken, String org, String login) {
    try {
      HttpHeaders headers = githubHeaders(accessToken);
      ResponseEntity<Void> response = restTemplate.exchange(
          "https://api.github.com/orgs/" + encode(org) + "/members/" + encode(login),
          HttpMethod.GET,
          new HttpEntity<>(headers),
          Void.class);
      return response.getStatusCode().is2xxSuccessful();
    } catch (Exception e) {
      return false;
    }
  }

  private boolean isTeamMember(String accessToken, String org, String team, String login) {
    try {
      HttpHeaders headers = githubHeaders(accessToken);
      @SuppressWarnings("unchecked")
      Map<String, Object> body = restTemplate.exchange(
          "https://api.github.com/orgs/" + encode(org) + "/teams/" + encode(team)
              + "/memberships/" + encode(login),
          HttpMethod.GET,
          new HttpEntity<>(headers),
          Map.class).getBody();
      return body != null && "active".equals(body.get("state"));
    } catch (Exception e) {
      return false;
    }
  }

  private static HttpHeaders githubHeaders(String accessToken) {
    HttpHeaders headers = new HttpHeaders();
    headers.setBearerAuth(accessToken);
    headers.setAccept(java.util.List.of(MediaType.APPLICATION_JSON));
    return headers;
  }

  private static String encode(String value) {
    return URLEncoder.encode(value, StandardCharsets.UTF_8);
  }
}
