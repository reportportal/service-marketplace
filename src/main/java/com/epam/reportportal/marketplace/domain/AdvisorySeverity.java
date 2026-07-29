package com.epam.reportportal.marketplace.domain;

import com.fasterxml.jackson.annotation.JsonCreator;
import com.fasterxml.jackson.annotation.JsonValue;

public enum AdvisorySeverity {
  LOW,
  MEDIUM,
  HIGH,
  CRITICAL;

  @JsonValue
  public String value() {
    return name().toLowerCase();
  }

  @JsonCreator
  public static AdvisorySeverity fromValue(String value) {
    if (value == null || value.isBlank()) {
      throw new IllegalArgumentException("Advisory severity is required");
    }
    return AdvisorySeverity.valueOf(value.trim().toUpperCase());
  }
}
