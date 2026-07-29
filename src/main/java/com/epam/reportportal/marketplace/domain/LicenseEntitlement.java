package com.epam.reportportal.marketplace.domain;

import com.fasterxml.jackson.annotation.JsonInclude;
import com.fasterxml.jackson.annotation.JsonProperty;
import java.time.Instant;
import java.time.LocalDate;
import java.time.ZoneOffset;
import java.util.ArrayList;
import java.util.List;

@JsonInclude(JsonInclude.Include.NON_NULL)
public class LicenseEntitlement {

  private String customerId;
  private String tier = "premium";

  @JsonProperty("publicKeys")
  private List<LicensePublicKey> keys = new ArrayList<>();

  private Instant createdAt;
  private LocalDate expiresAt;

  public String getCustomerId() {
    return customerId;
  }

  public void setCustomerId(String customerId) {
    this.customerId = customerId;
  }

  public String getTier() {
    return tier;
  }

  public void setTier(String tier) {
    this.tier = tier;
  }

  public List<LicensePublicKey> getKeys() {
    return keys;
  }

  public void setKeys(List<LicensePublicKey> keys) {
    this.keys = keys;
  }

  public Instant getCreatedAt() {
    return createdAt;
  }

  public void setCreatedAt(Instant createdAt) {
    this.createdAt = createdAt;
  }

  @JsonProperty("issuedAt")
  public LocalDate getIssuedAt() {
    return createdAt != null ? createdAt.atZone(ZoneOffset.UTC).toLocalDate() : null;
  }

  public LocalDate getExpiresAt() {
    return expiresAt;
  }

  public void setExpiresAt(LocalDate expiresAt) {
    this.expiresAt = expiresAt;
  }
}
