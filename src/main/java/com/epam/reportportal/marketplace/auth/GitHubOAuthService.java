package com.epam.reportportal.marketplace.auth;

import com.epam.reportportal.marketplace.config.MarketplaceProperties;
import com.epam.reportportal.marketplace.web.dto.AuthTokenResponseDto;
import com.epam.reportportal.marketplace.web.error.ForbiddenException;
import com.epam.reportportal.marketplace.web.error.UnauthorizedException;
import java.net.URI;
import java.net.URLEncoder;
import java.nio.charset.StandardCharsets;
import java.util.Map;
import java.util.UUID;
import java.util.concurrent.ConcurrentHashMap;
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

  private final MarketplaceProperties properties;
  private final SessionJwtService sessionJwtService;
  private final RestTemplate restTemplate;
  private final Map<String, InstantHolder> states = new ConcurrentHashMap<>();

  public GitHubOAuthService(
      MarketplaceProperties properties,
      SessionJwtService sessionJwtService,
      RestTemplateBuilder restTemplateBuilder) {
    this.properties = properties;
    this.sessionJwtService = sessionJwtService;
    this.restTemplate = restTemplateBuilder.build();
  }

  public URI buildLoginRedirect() {
    String clientId = properties.getAuth().getGithub().getClientId();
    if (clientId == null || clientId.isBlank()) {
      throw new ForbiddenException("GitHub OAuth is not configured");
    }
    String state = UUID.randomUUID().toString();
    states.put(state, new InstantHolder(System.currentTimeMillis()));
    return UriComponentsBuilder.fromUriString("https://github.com/login/oauth/authorize")
        .queryParam("client_id", clientId)
        .queryParam("redirect_uri", properties.getAuth().getGithub().getRedirectUri())
        .queryParam("scope", "read:org")
        .queryParam("state", state)
        .build(true)
        .toUri();
  }

  public AuthTokenResponseDto handleCallback(String code, String state) {
    if (!states.containsKey(state)) {
      throw new UnauthorizedException("Invalid OAuth state");
    }
    states.remove(state);
    String clientId = properties.getAuth().getGithub().getClientId();
    if (clientId == null || clientId.isBlank()) {
      return mockLogin();
    }
    String accessToken = exchangeCode(code);
    String login = fetchUserLogin(accessToken);
    if (!isMember(accessToken, login)) {
      throw new ForbiddenException("GitHub membership check failed");
    }
    return issueToken(login);
  }

  public AuthTokenResponseDto mockLogin() {
    return issueToken("github-mock-operator");
  }

  private AuthTokenResponseDto issueToken(String subject) {
    String token = sessionJwtService.createToken(subject);
    return new AuthTokenResponseDto(token, "Bearer", sessionJwtService.getTtlSeconds());
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

  private boolean isMember(String accessToken, String login) {
    String org = properties.getAuth().getGithub().getAllowedOrg();
    if (org == null || org.isBlank()) {
      return true;
    }
    try {
      HttpHeaders headers = new HttpHeaders();
      headers.setBearerAuth(accessToken);
      headers.setAccept(java.util.List.of(MediaType.APPLICATION_JSON));
      ResponseEntity<Void> response = restTemplate.exchange(
          "https://api.github.com/orgs/" + URLEncoder.encode(org, StandardCharsets.UTF_8) + "/members/"
              + URLEncoder.encode(login, StandardCharsets.UTF_8),
          HttpMethod.GET,
          new HttpEntity<>(headers),
          Void.class);
      return response.getStatusCode().is2xxSuccessful();
    } catch (Exception e) {
      return false;
    }
  }

  private record InstantHolder(long createdAt) {}
}
