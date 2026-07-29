package com.epam.reportportal.marketplace.config;

import java.util.HashMap;
import java.util.Map;
import org.springframework.boot.context.properties.ConfigurationProperties;

@ConfigurationProperties(prefix = "marketplace")
public class MarketplaceProperties {

  private Storage storage = new Storage();
  private Gcs gcs = new Gcs();
  private Cdn cdn = new Cdn();
  private Auth auth = new Auth();
  private PublishOidcTrust publishOidcTrust = new PublishOidcTrust();

  public Storage getStorage() {
    return storage;
  }

  public void setStorage(Storage storage) {
    this.storage = storage;
  }

  public Gcs getGcs() {
    return gcs;
  }

  public void setGcs(Gcs gcs) {
    this.gcs = gcs;
  }

  public Cdn getCdn() {
    return cdn;
  }

  public void setCdn(Cdn cdn) {
    this.cdn = cdn;
  }

  public Auth getAuth() {
    return auth;
  }

  public void setAuth(Auth auth) {
    this.auth = auth;
  }

  public PublishOidcTrust getPublishOidcTrust() {
    return publishOidcTrust;
  }

  public void setPublishOidcTrust(PublishOidcTrust publishOidcTrust) {
    this.publishOidcTrust = publishOidcTrust;
  }

  public static class Storage {
    private String type = "local";
    private Local local = new Local();

    public String getType() {
      return type;
    }

    public void setType(String type) {
      this.type = type;
    }

    public Local getLocal() {
      return local;
    }

    public void setLocal(Local local) {
      this.local = local;
    }
  }

  public static class Local {
    private String root = "./data/marketplace";

    public String getRoot() {
      return root;
    }

    public void setRoot(String root) {
      this.root = root;
    }
  }

  public static class Gcs {
    private String bucket;
    private String privateBucket;
    private String location = "US";

    public String getBucket() {
      return bucket;
    }

    public void setBucket(String bucket) {
      this.bucket = bucket;
    }

    public String getPrivateBucket() {
      return privateBucket;
    }

    public void setPrivateBucket(String privateBucket) {
      this.privateBucket = privateBucket;
    }

    public String getLocation() {
      return location;
    }

    public void setLocation(String location) {
      this.location = location;
    }
  }

  public static class Cdn {
    private String baseUrl = "http://localhost:8080/cdn";
    private String urlMap;

    public String getBaseUrl() {
      return baseUrl;
    }

    public void setBaseUrl(String baseUrl) {
      this.baseUrl = baseUrl;
    }

    public String getUrlMap() {
      return urlMap;
    }

    public void setUrlMap(String urlMap) {
      this.urlMap = urlMap;
    }
  }

  public static class Auth {
    private Admin admin = new Admin();
    private Jwt jwt = new Jwt();
    private GitHub github = new GitHub();
    private LoginRateLimit loginRateLimit = new LoginRateLimit();

    public Admin getAdmin() {
      return admin;
    }

    public void setAdmin(Admin admin) {
      this.admin = admin;
    }

    public Jwt getJwt() {
      return jwt;
    }

    public void setJwt(Jwt jwt) {
      this.jwt = jwt;
    }

    public GitHub getGithub() {
      return github;
    }

    public void setGithub(GitHub github) {
      this.github = github;
    }

    public LoginRateLimit getLoginRateLimit() {
      return loginRateLimit;
    }

    public void setLoginRateLimit(LoginRateLimit loginRateLimit) {
      this.loginRateLimit = loginRateLimit;
    }
  }

  public static class LoginRateLimit {
    private boolean enabled = true;
    private int maxAttempts = 5;
    private long windowSeconds = 300;
    private long lockoutSeconds = 60;
    private double backoffMultiplier = 2.0;
    private long maxLockoutSeconds = 900;

    public boolean isEnabled() {
      return enabled;
    }

    public void setEnabled(boolean enabled) {
      this.enabled = enabled;
    }

    public int getMaxAttempts() {
      return maxAttempts;
    }

    public void setMaxAttempts(int maxAttempts) {
      this.maxAttempts = maxAttempts;
    }

    public long getWindowSeconds() {
      return windowSeconds;
    }

    public void setWindowSeconds(long windowSeconds) {
      this.windowSeconds = windowSeconds;
    }

    public long getLockoutSeconds() {
      return lockoutSeconds;
    }

    public void setLockoutSeconds(long lockoutSeconds) {
      this.lockoutSeconds = lockoutSeconds;
    }

    public double getBackoffMultiplier() {
      return backoffMultiplier;
    }

    public void setBackoffMultiplier(double backoffMultiplier) {
      this.backoffMultiplier = backoffMultiplier;
    }

    public long getMaxLockoutSeconds() {
      return maxLockoutSeconds;
    }

    public void setMaxLockoutSeconds(long maxLockoutSeconds) {
      this.maxLockoutSeconds = maxLockoutSeconds;
    }
  }

  public static class Admin {
    private String username = "admin";
    private String passwordHash;

    public String getUsername() {
      return username;
    }

    public void setUsername(String username) {
      this.username = username;
    }

    public String getPasswordHash() {
      return passwordHash;
    }

    public void setPasswordHash(String passwordHash) {
      this.passwordHash = passwordHash;
    }
  }

  public static class Jwt {
    private String secret;
    private String issuer = "marketplace.reportportal.io";
    private long ttlSeconds = 28800;

    public String getSecret() {
      return secret;
    }

    public void setSecret(String secret) {
      this.secret = secret;
    }

    public String getIssuer() {
      return issuer;
    }

    public void setIssuer(String issuer) {
      this.issuer = issuer;
    }

    public long getTtlSeconds() {
      return ttlSeconds;
    }

    public void setTtlSeconds(long ttlSeconds) {
      this.ttlSeconds = ttlSeconds;
    }
  }

  public static class GitHub {
    private String clientId = "";
    private String clientSecret = "";
    private String allowedOrg = "reportportal";
    private String allowedTeam = "";
    private String redirectUri;
    private long oauthStateTtlSeconds = 600;

    public String getClientId() {
      return clientId;
    }

    public void setClientId(String clientId) {
      this.clientId = clientId;
    }

    public String getClientSecret() {
      return clientSecret;
    }

    public void setClientSecret(String clientSecret) {
      this.clientSecret = clientSecret;
    }

    public String getAllowedOrg() {
      return allowedOrg;
    }

    public void setAllowedOrg(String allowedOrg) {
      this.allowedOrg = allowedOrg;
    }

    public String getAllowedTeam() {
      return allowedTeam;
    }

    public void setAllowedTeam(String allowedTeam) {
      this.allowedTeam = allowedTeam;
    }

    public String getRedirectUri() {
      return redirectUri;
    }

    public void setRedirectUri(String redirectUri) {
      this.redirectUri = redirectUri;
    }

    public long getOauthStateTtlSeconds() {
      return oauthStateTtlSeconds;
    }

    public void setOauthStateTtlSeconds(long oauthStateTtlSeconds) {
      this.oauthStateTtlSeconds = oauthStateTtlSeconds;
    }
  }

  public static class PublishOidcTrust {
    private String audience;
    private String issuer = "https://token.actions.githubusercontent.com";
    private Map<String, String> allowedSources = new HashMap<>();

    public String getAudience() {
      return audience;
    }

    public void setAudience(String audience) {
      this.audience = audience;
    }

    public String getIssuer() {
      return issuer;
    }

    public void setIssuer(String issuer) {
      this.issuer = issuer;
    }

    public Map<String, String> getAllowedSources() {
      return allowedSources;
    }

    public void setAllowedSources(Map<String, String> allowedSources) {
      this.allowedSources = allowedSources;
    }
  }
}
