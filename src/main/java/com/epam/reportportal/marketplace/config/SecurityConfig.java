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

@Configuration
@EnableWebSecurity
public class SecurityConfig {

  private final BearerAuthenticationFilter bearerAuthenticationFilter;

  public SecurityConfig(BearerAuthenticationFilter bearerAuthenticationFilter) {
    this.bearerAuthenticationFilter = bearerAuthenticationFilter;
  }

  @Bean
  SecurityFilterChain securityFilterChain(HttpSecurity http) throws Exception {
    http.csrf(csrf -> csrf.disable())
        .sessionManagement(session -> session.sessionCreationPolicy(SessionCreationPolicy.STATELESS))
        .authorizeHttpRequests(auth -> auth
            .requestMatchers(HttpMethod.GET, "/api/v1/plugins/**").permitAll()
            .requestMatchers(HttpMethod.GET, "/cdn/**").permitAll()
            .requestMatchers("/actuator/health", "/actuator/health/**", "/actuator/info").permitAll()
            .requestMatchers("/api/v1/auth/**").permitAll()
            .requestMatchers("/", "/index.html", "/operator", "/operator/**").permitAll()
            .anyRequest().hasAnyRole("OPERATOR", "OIDC_PUBLISH"))
        .addFilterBefore(bearerAuthenticationFilter, UsernamePasswordAuthenticationFilter.class);
    return http.build();
  }

  @Bean
  PasswordEncoder passwordEncoder() {
    return new BCryptPasswordEncoder();
  }
}
