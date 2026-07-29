package com.epam.reportportal.marketplace.web;

import com.epam.reportportal.marketplace.auth.AdminAuthService;
import com.epam.reportportal.marketplace.auth.GitHubOAuthService;
import com.epam.reportportal.marketplace.auth.SessionJwtService;
import com.epam.reportportal.marketplace.web.dto.AdminLoginRequestDto;
import com.epam.reportportal.marketplace.web.dto.AuthTokenResponseDto;
import com.epam.reportportal.marketplace.web.error.UnauthorizedException;
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
  private final SessionJwtService sessionJwtService;

  public AuthController(
      GitHubOAuthService gitHubOAuthService,
      AdminAuthService adminAuthService,
      SessionJwtService sessionJwtService) {
    this.gitHubOAuthService = gitHubOAuthService;
    this.adminAuthService = adminAuthService;
    this.sessionJwtService = sessionJwtService;
  }

  @GetMapping("/github/login")
  ResponseEntity<Void> githubLogin() {
    URI location = gitHubOAuthService.buildLoginRedirect();
    return ResponseEntity.status(HttpStatus.FOUND).header(HttpHeaders.LOCATION, location.toString()).build();
  }

  @GetMapping("/github/callback")
  ResponseEntity<?> githubCallback(@RequestParam String code, @RequestParam String state) {
    AuthTokenResponseDto token = gitHubOAuthService.handleCallback(code, state);
    // Browser Operator UI expects a redirect with the session JWT
    URI operator = URI.create("/operator/?token=" + token.accessToken());
    return ResponseEntity.status(HttpStatus.FOUND)
        .header(HttpHeaders.LOCATION, operator.toString())
        .body(token);
  }

  @PostMapping("/login")
  AuthTokenResponseDto adminLogin(@Valid @RequestBody AdminLoginRequestDto request) {
    if (!adminAuthService.authenticate(request.username(), request.password())) {
      throw new UnauthorizedException("Invalid credentials");
    }
    String token = sessionJwtService.createToken(request.username());
    return new AuthTokenResponseDto(token, "Bearer", sessionJwtService.getTtlSeconds());
  }

  @PostMapping("/logout")
  ResponseEntity<Void> logout() {
    return ResponseEntity.noContent().build();
  }
}
