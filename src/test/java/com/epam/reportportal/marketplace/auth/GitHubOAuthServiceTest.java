package com.epam.reportportal.marketplace.auth;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.springframework.test.web.client.match.MockRestRequestMatchers.method;
import static org.springframework.test.web.client.match.MockRestRequestMatchers.requestTo;
import static org.springframework.test.web.client.response.MockRestResponseCreators.withStatus;
import static org.springframework.test.web.client.response.MockRestResponseCreators.withSuccess;

import com.epam.reportportal.marketplace.config.MarketplaceProperties;
import com.epam.reportportal.marketplace.web.dto.AuthTokenResponseDto;
import com.epam.reportportal.marketplace.web.error.ForbiddenException;
import com.epam.reportportal.marketplace.web.error.UnauthorizedException;
import jakarta.servlet.http.Cookie;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.springframework.boot.restclient.RestTemplateBuilder;
import org.springframework.http.HttpMethod;
import org.springframework.http.HttpStatus;
import org.springframework.http.MediaType;
import org.springframework.mock.web.MockHttpServletRequest;
import org.springframework.mock.web.MockHttpServletResponse;
import org.springframework.test.web.client.MockRestServiceServer;

class GitHubOAuthServiceTest {

  private MarketplaceProperties properties;
  private MockRestServiceServer server;
  private GitHubOAuthService service;

  @BeforeEach
  void setUp() {
    properties = new MarketplaceProperties();
    properties.getAuth().getJwt().setSecret("test-secret-key-at-least-32-characters-long");
    properties.getAuth().getJwt().setIssuer("marketplace.reportportal.io");
    properties.getAuth().getGithub().setClientId("client");
    properties.getAuth().getGithub().setClientSecret("secret");
    properties.getAuth().getGithub().setRedirectUri("http://localhost/callback");
    properties.getAuth().getGithub().setAllowedOrg("reportportal");
    properties.getAuth().getGithub().setAllowedTeam("");
    properties.getAuth().getGithub().setOauthStateTtlSeconds(600);

    service = new GitHubOAuthService(
        properties, new SessionJwtService(properties), new RestTemplateBuilder());
    server = MockRestServiceServer.bindTo(service.restTemplate).build();
  }

  @Test
  void loginRedirectSetsSignedStateCookieAndQueryParam() {
    MockHttpServletRequest request = new MockHttpServletRequest();
    MockHttpServletResponse response = new MockHttpServletResponse();

    var location = service.buildLoginRedirect(request, response);

    Cookie stateCookie = response.getCookie(OAuthStateCookie.NAME);
    assertTrue(stateCookie != null && stateCookie.isHttpOnly());
    assertEquals("/api/v1/auth/github", stateCookie.getPath());
    assertTrue(location.getQuery().contains("state=" + stateCookie.getValue()));
    assertTrue(location.getQuery().contains("scope=read:org"));
  }

  @Test
  void callbackRejectsMismatchedCookieAndQueryState() {
    MockHttpServletRequest request = new MockHttpServletRequest();
    request.setCookies(new Cookie(OAuthStateCookie.NAME, service.createStateToken(600)));
    MockHttpServletResponse response = new MockHttpServletResponse();

    assertThrows(
        UnauthorizedException.class,
        () -> service.handleCallback("code", service.createStateToken(600), request, response));
  }

  @Test
  void callbackRejectsExpiredState() throws Exception {
    String expired = service.createStateToken(0);
    Thread.sleep(1100);
    MockHttpServletRequest request = new MockHttpServletRequest();
    request.setCookies(new Cookie(OAuthStateCookie.NAME, expired));
    MockHttpServletResponse response = new MockHttpServletResponse();

    assertThrows(
        UnauthorizedException.class,
        () -> service.handleCallback("code", expired, request, response));
  }

  @Test
  void teamMemberSucceedsWhenTeamConfigured() {
    properties.getAuth().getGithub().setAllowedTeam("core-team");
    expectSuccessfulGithubFlow("alice", true, "active");

    AuthTokenResponseDto token = completeCallback();

    assertEquals("Bearer", token.tokenType());
    assertTrue(token.accessToken() != null && !token.accessToken().isBlank());
    server.verify();
  }

  @Test
  void orgMemberNotOnTeamIsForbidden() {
    properties.getAuth().getGithub().setAllowedTeam("core-team");
    expectSuccessfulGithubFlow("alice", true, "pending");

    assertThrows(ForbiddenException.class, this::completeCallback);
    server.verify();
  }

  @Test
  void teamApiErrorIsForbidden() {
    properties.getAuth().getGithub().setAllowedTeam("core-team");
    expectTokenAndUser("alice");
    server.expect(requestTo("https://api.github.com/orgs/reportportal/members/alice"))
        .andExpect(method(HttpMethod.GET))
        .andRespond(withStatus(HttpStatus.NO_CONTENT));
    server.expect(requestTo(
            "https://api.github.com/orgs/reportportal/teams/core-team/memberships/alice"))
        .andExpect(method(HttpMethod.GET))
        .andRespond(withStatus(HttpStatus.NOT_FOUND));

    assertThrows(ForbiddenException.class, this::completeCallback);
    server.verify();
  }

  @Test
  void teamCheckSkippedWhenTeamBlank() {
    expectTokenAndUser("alice");
    server.expect(requestTo("https://api.github.com/orgs/reportportal/members/alice"))
        .andExpect(method(HttpMethod.GET))
        .andRespond(withStatus(HttpStatus.NO_CONTENT));

    AuthTokenResponseDto token = completeCallback();

    assertTrue(token.accessToken() != null && !token.accessToken().isBlank());
    server.verify();
  }

  @Test
  void teamWithoutOrgIsForbidden() {
    properties.getAuth().getGithub().setAllowedOrg("");
    properties.getAuth().getGithub().setAllowedTeam("core-team");
    expectTokenAndUser("alice");

    assertThrows(ForbiddenException.class, this::completeCallback);
    server.verify();
  }

  @Test
  void signedStateValidatesAcrossServiceInstances() {
    GitHubOAuthService other = new GitHubOAuthService(
        properties, new SessionJwtService(properties), new RestTemplateBuilder());
    String state = service.createStateToken(600);
    other.validateState(state, state);
  }

  private AuthTokenResponseDto completeCallback() {
    String state = service.createStateToken(600);
    MockHttpServletRequest request = new MockHttpServletRequest();
    request.setCookies(new Cookie(OAuthStateCookie.NAME, state));
    MockHttpServletResponse response = new MockHttpServletResponse();
    AuthTokenResponseDto token = service.handleCallback("auth-code", state, request, response);
    Cookie cleared = response.getCookie(OAuthStateCookie.NAME);
    assertTrue(cleared != null && cleared.getMaxAge() == 0);
    return token;
  }

  private void expectSuccessfulGithubFlow(String login, boolean orgMember, String teamState) {
    expectTokenAndUser(login);
    server.expect(requestTo("https://api.github.com/orgs/reportportal/members/" + login))
        .andExpect(method(HttpMethod.GET))
        .andRespond(orgMember
            ? withStatus(HttpStatus.NO_CONTENT)
            : withStatus(HttpStatus.NOT_FOUND));
    server.expect(requestTo(
            "https://api.github.com/orgs/reportportal/teams/core-team/memberships/" + login))
        .andExpect(method(HttpMethod.GET))
        .andRespond(withSuccess(
            "{\"state\":\"" + teamState + "\",\"role\":\"member\"}", MediaType.APPLICATION_JSON));
  }

  private void expectTokenAndUser(String login) {
    server.expect(requestTo("https://github.com/login/oauth/access_token"))
        .andExpect(method(HttpMethod.POST))
        .andRespond(withSuccess(
            "{\"access_token\":\"gho_test\",\"token_type\":\"bearer\"}", MediaType.APPLICATION_JSON));
    server.expect(requestTo("https://api.github.com/user"))
        .andExpect(method(HttpMethod.GET))
        .andRespond(withSuccess("{\"login\":\"" + login + "\"}", MediaType.APPLICATION_JSON));
  }
}
