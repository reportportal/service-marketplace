package com.epam.reportportal.marketplace.domain;

import com.fasterxml.jackson.annotation.JsonCreator;
import com.fasterxml.jackson.annotation.JsonValue;

public enum PluginCategory {
  BUG_TRACKING("bug-tracking"),
  NOTIFICATIONS("notifications"),
  AUTHORIZATION("authorization"),
  IMPORT("import");

  private final String value;

  PluginCategory(String value) {
    this.value = value;
  }

  @JsonValue
  public String value() {
    return value;
  }

  @JsonCreator
  public static PluginCategory fromValue(String value) {
    for (PluginCategory category : values()) {
      if (category.value.equals(value)) {
        return category;
      }
    }
    throw new IllegalArgumentException("Unknown category: " + value);
  }
}
