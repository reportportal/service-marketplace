package com.epam.reportportal.marketplace.domain;

import com.fasterxml.jackson.annotation.JsonCreator;
import com.fasterxml.jackson.annotation.JsonValue;

public enum TrustTier {
  OFFICIAL("official"),
  PARTNER("partner");

  private final String value;

  TrustTier(String value) {
    this.value = value;
  }

  @JsonValue
  public String value() {
    return value;
  }

  @JsonCreator
  public static TrustTier fromValue(String value) {
    for (TrustTier tier : values()) {
      if (tier.value.equalsIgnoreCase(value)) {
        return tier;
      }
    }
    throw new IllegalArgumentException("Unknown trust tier: " + value);
  }
}
