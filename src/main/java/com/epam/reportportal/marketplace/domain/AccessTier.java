package com.epam.reportportal.marketplace.domain;

import com.fasterxml.jackson.annotation.JsonCreator;
import com.fasterxml.jackson.annotation.JsonValue;

public enum AccessTier {
  PUBLIC("public"),
  PREMIUM("premium");

  private final String value;

  AccessTier(String value) {
    this.value = value;
  }

  @JsonValue
  public String value() {
    return value;
  }

  @JsonCreator
  public static AccessTier fromValue(String value) {
    if (value == null) {
      return PUBLIC;
    }
    for (AccessTier tier : values()) {
      if (tier.value.equalsIgnoreCase(value)) {
        return tier;
      }
    }
    throw new IllegalArgumentException("Unknown access tier: " + value);
  }
}
