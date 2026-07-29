package com.epam.reportportal.marketplace.auth;

import jakarta.servlet.FilterChain;
import jakarta.servlet.ServletException;
import jakarta.servlet.http.HttpServletRequest;
import jakarta.servlet.http.HttpServletResponse;
import java.io.IOException;
import java.util.List;
import org.springframework.security.authentication.UsernamePasswordAuthenticationToken;
import org.springframework.security.core.authority.SimpleGrantedAuthority;
import org.springframework.security.core.context.SecurityContextHolder;
import org.springframework.stereotype.Component;
import org.springframework.web.filter.OncePerRequestFilter;

@Component
public class BearerAuthenticationFilter extends OncePerRequestFilter {

  private final SessionJwtService sessionJwtService;
  private final OidcPublishAuthService oidcPublishAuthService;

  public BearerAuthenticationFilter(
      SessionJwtService sessionJwtService,
      OidcPublishAuthService oidcPublishAuthService) {
    this.sessionJwtService = sessionJwtService;
    this.oidcPublishAuthService = oidcPublishAuthService;
  }

  @Override
  protected void doFilterInternal(
      HttpServletRequest request, HttpServletResponse response, FilterChain filterChain)
      throws ServletException, IOException {
    String header = request.getHeader("Authorization");
    if (header != null && header.startsWith("Bearer ")) {
      String token = header.substring(7);
      if (oidcPublishAuthService.isOidcToken(token)) {
        try {
          // Signature + issuer + audience verified; repo→plugin allow-list checked at publish time
          oidcPublishAuthService.validatePublishToken(token, null);
          SecurityContextHolder.getContext().setAuthentication(
              new UsernamePasswordAuthenticationToken(
                  "github-actions", null, List.of(new SimpleGrantedAuthority("ROLE_OIDC_PUBLISH"))));
        } catch (RuntimeException ignored) {
          // Leave unauthenticated; authorization layer will reject protected routes
        }
      } else {
        SessionJwtService.SessionPrincipal principal = sessionJwtService.validateToken(token);
        if (principal != null && principal.isOperator()) {
          SecurityContextHolder.getContext().setAuthentication(
              new UsernamePasswordAuthenticationToken(
                  principal.subject(),
                  null,
                  List.of(new SimpleGrantedAuthority("ROLE_OPERATOR"))));
        }
      }
    }
    filterChain.doFilter(request, response);
  }
}
