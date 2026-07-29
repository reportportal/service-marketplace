package com.epam.reportportal.marketplace.auth;

import com.epam.reportportal.marketplace.config.MarketplaceProperties;
import org.springframework.security.crypto.bcrypt.BCryptPasswordEncoder;
import org.springframework.stereotype.Service;

@Service
public class AdminAuthService {

  /**
   * Valid bcrypt hash used only to keep verification time roughly constant when the username is
   * wrong or the configured hash is missing. The result is discarded unless a real hash is set.
   */
  private static final String DUMMY_BCRYPT_HASH =
      "$2a$10$bbdLQFSI9d1QNOxWBerBceaVlOSl30P8PGK7i7bPG9bQ8jOVQIrja";

  private final MarketplaceProperties properties;
  private final BCryptPasswordEncoder passwordEncoder = new BCryptPasswordEncoder();

  public AdminAuthService(MarketplaceProperties properties) {
    this.properties = properties;
  }

  public boolean authenticate(String username, String password) {
    if (username == null || password == null) {
      return false;
    }
    String configuredUsername = properties.getAuth().getAdmin().getUsername();
    String hash = properties.getAuth().getAdmin().getPasswordHash();
    boolean usernameMatches =
        configuredUsername != null && username.equals(configuredUsername);
    String hashToCheck = (hash != null && !hash.isBlank()) ? hash : DUMMY_BCRYPT_HASH;
    // Always run bcrypt so a wrong username does not short-circuit before the expensive compare.
    boolean passwordMatches = passwordEncoder.matches(password, hashToCheck);
    return usernameMatches && hash != null && !hash.isBlank() && passwordMatches;
  }
}
