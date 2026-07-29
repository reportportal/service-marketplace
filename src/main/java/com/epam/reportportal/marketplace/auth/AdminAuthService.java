package com.epam.reportportal.marketplace.auth;

import com.epam.reportportal.marketplace.config.MarketplaceProperties;
import org.springframework.security.crypto.bcrypt.BCryptPasswordEncoder;
import org.springframework.stereotype.Service;

@Service
public class AdminAuthService {

  private final MarketplaceProperties properties;
  private final BCryptPasswordEncoder passwordEncoder = new BCryptPasswordEncoder();

  public AdminAuthService(MarketplaceProperties properties) {
    this.properties = properties;
  }

  public boolean authenticate(String username, String password) {
    if (username == null || password == null) {
      return false;
    }
    if (!username.equals(properties.getAuth().getAdmin().getUsername())) {
      return false;
    }
    String hash = properties.getAuth().getAdmin().getPasswordHash();
    if (hash != null && !hash.isBlank()) {
      return passwordEncoder.matches(password, hash);
    }
    String plain = properties.getAuth().getAdmin().getPassword();
    return plain != null && plain.equals(password);
  }
}
