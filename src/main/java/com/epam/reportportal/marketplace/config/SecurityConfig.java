package com.epam.reportportal.marketplace.config;

import com.epam.reportportal.marketplace.auth.BearerAuthenticationFilter;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;
import org.springframework.http.HttpMethod;
import org.springframework.security.config.annotation.web.builders.HttpSecurity;
import org.springframework.security.config.annotation.web.configuration.EnableWebSecurity;
import org.springframework.security.config.http.SessionCreationPolicy;
import org.springframework.security.crypto.bcrypt.BCryptPasswordEncoder;
import org.springframework.security.crypto.password.PasswordEncoder;
import org.springframework.security.web.SecurityFilterChain;
import org.springframework.security.web.authentication.UsernamePasswordAuthenticationFilter;
import org.springframework.security.web.header.writers.ReferrerPolicyHeaderWriter;

@Configuration
@EnableWebSecurity
public class SecurityConfig {

  private final BearerAuthenticationFilter bearerAuthenticationFilter;

  public SecurityConfig(BearerAuthenticationFilter bearerAuthenticationFilter) {
    this.bearerAuthenticationFilter = bearerAuthenticationFilter;
  }

  private static final String OPERATOR_CSP =
      "default-src 'self'; "
          + "script-src 'self'; "
          + "style-src 'self'; "
          + "img-src 'self' data:; "
          + "connect-src 'self'; "
          + "object-src 'none'; "
          + "base-uri 'none'; "
          + "frame-ancestors 'none'; "
          + "form-action 'self'";

  @Bean
  SecurityFilterChain securityFilterChain(HttpSecurity http) throws Exception {
    http.csrf(csrf -> csrf.disable())
        .sessionManagement(session -> session.sessionCreationPolicy(SessionCreationPolicy.STATELESS))
        .headers(headers -> headers
            .contentSecurityPolicy(csp -> csp.policyDirectives(OPERATOR_CSP))
            .frameOptions(frame -> frame.deny())
            .referrerPolicy(referrer ->
                referrer.policy(ReferrerPolicyHeaderWriter.ReferrerPolicy.STRICT_ORIGIN_WHEN_CROSS_ORIGIN)))
        .authorizeHttpRequests(auth -> auth
            .requestMatchers(HttpMethod.GET, "/api/v1/plugins/**").permitAll()
            .requestMatchers(HttpMethod.GET, "/cdn/**", "/cdn-private/**").permitAll()
            .requestMatchers("/actuator/health", "/actuator/health/**", "/actuator/info").permitAll()
            .requestMatchers("/api/v1/auth/**").permitAll()
            .requestMatchers("/", "/index.html", "/operator", "/operator/**").permitAll()
            .requestMatchers(
                HttpMethod.POST,
                "/api/v1/plugins",
                "/api/v1/plugins/*/versions")
            .hasAnyRole("OPERATOR", "OIDC_PUBLISH")
            .anyRequest().hasRole("OPERATOR"))
        .addFilterBefore(bearerAuthenticationFilter, UsernamePasswordAuthenticationFilter.class);
    return http.build();
  }

  @Bean
  PasswordEncoder passwordEncoder() {
    return new BCryptPasswordEncoder();
  }
}
