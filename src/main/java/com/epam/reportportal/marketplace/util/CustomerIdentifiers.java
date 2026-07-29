package com.epam.reportportal.marketplace.util;

import java.util.regex.Pattern;

/**
 * Customer id shape for license entitlements: lowercase slug, path-safe, mirrors plugin id rules.
 */
public final class CustomerIdentifiers {

  public static final String ID_REGEX = PluginIdentifiers.ID_REGEX;

  private static final Pattern ID_PATTERN = Pattern.compile(ID_REGEX);

  private CustomerIdentifiers() {}

  public static boolean isValidId(String customerId) {
    return customerId != null
        && ID_PATTERN.matcher(customerId).matches()
        && PluginIdentifiers.isPathSafeSegment(customerId);
  }

  public static String idRequirement() {
    return "must match " + ID_REGEX + " and must not contain path separators or '..'";
  }
}
