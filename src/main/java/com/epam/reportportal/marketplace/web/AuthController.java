package com.epam.reportportal.marketplace.web;

import com.epam.reportportal.marketplace.auth.AdminAuthService;
import com.epam.reportportal.marketplace.auth.AdminLoginRateLimiter;
import com.epam.reportportal.marketplace.auth.GitHubOAuthService;
import com.epam.reportportal.marketplace.auth.OperatorSessionCookie;
import com.epam.reportportal.marketplace.auth.SessionJwtService;
import com.epam.reportportal.marketplace.web.dto.AdminLoginRequestDto;
import com.epam.reportportal.marketplace.web.dto.AuthTokenResponseDto;
import com.epam.reportportal.marketplace.web.error.UnauthorizedException;
import jakarta.servlet.http.HttpServletRequest;
import jakarta.servlet.http.HttpServletResponse;
import jakarta.validation.Valid;
import java.net.URI;
import org.springframework.http.HttpHeaders;
import org.springframework.http.HttpStatus;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

@RestController
@RequestMapping("/api/v1/auth")
public class AuthController {

  private final GitHubOAuthService gitHubOAuthService;
  private final AdminAuthService adminAuthService;
  private final AdminLoginRateLimiter adminLoginRateLimiter;
  private final SessionJwtService sessionJwtService;

  public AuthController(
      GitHubOAuthService gitHubOAuthService,
      AdminAuthService adminAuthService,
      AdminLoginRateLimiter adminLoginRateLimiter,
      SessionJwtService sessionJwtService) {
    this.gitHubOAuthService = gitHubOAuthService;
    this.adminAuthService = adminAuthService;
    this.adminLoginRateLimiter = adminLoginRateLimiter;
    this.sessionJwtService = sessionJwtService;
  }

  @GetMapping("/github/login")
  ResponseEntity<Void> githubLogin(HttpServletRequest request, HttpServletResponse response) {
    URI location = gitHubOAuthService.buildLoginRedirect(request, response);
    return ResponseEntity.status(HttpStatus.FOUND).header(HttpHeaders.LOCATION, location.toString()).build();
  }

  @GetMapping("/github/callback")
  ResponseEntity<Void> githubCallback(
      @RequestParam String code,
      @RequestParam String state,
      HttpServletRequest request,
      HttpServletResponse response) {
    AuthTokenResponseDto token = gitHubOAuthService.handleCallback(code, state, request, response);
    OperatorSessionCookie.set(request, response, token.accessToken(), token.expiresIn());
    // Token travels in the HttpOnly cookie only — never in the Location query string.
    return ResponseEntity.status(HttpStatus.FOUND)
        .header(HttpHeaders.LOCATION, "/operator/")
        .build();
  }

  @PostMapping("/login")
  AuthTokenResponseDto adminLogin(
      @Valid @RequestBody AdminLoginRequestDto requestBody,
      HttpServletRequest request,
      HttpServletResponse response) {
    adminLoginRateLimiter.checkAllowed(requestBody.username());
    if (!adminAuthService.authenticate(requestBody.username(), requestBody.password())) {
      adminLoginRateLimiter.recordFailure(requestBody.username());
      throw new UnauthorizedException("Invalid credentials");
    }
    adminLoginRateLimiter.recordSuccess(requestBody.username());
    String token = sessionJwtService.createToken(requestBody.username());
    long ttl = sessionJwtService.getTtlSeconds();
    OperatorSessionCookie.set(request, response, token, ttl);
    return new AuthTokenResponseDto(token, "Bearer", ttl);
  }

  @PostMapping("/logout")
  ResponseEntity<Void> logout(HttpServletRequest request, HttpServletResponse response) {
    OperatorSessionCookie.clear(request, response);
    return ResponseEntity.noContent().build();
  }
}
