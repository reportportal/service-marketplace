package com.epam.reportportal.marketplace.util;

import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.ValueSource;

class PluginIdentifiersTest {

  @ParameterizedTest
  @ValueSource(strings = {"plugin-jira", "plugin-jira-cloud", "a1", "ab", "plugin0"})
  void acceptsValidPluginIds(String pluginId) {
    assertTrue(PluginIdentifiers.isValidId(pluginId));
  }

  @ParameterizedTest
  @ValueSource(
      strings = {
        "",
        "A",
        "Plugin",
        "plugin_jira",
        "plugin/jira",
        "../plugin",
        "plugin..id",
        "-plugin",
        "plugin-",
        "p"
      })
  void rejectsInvalidPluginIds(String pluginId) {
    assertFalse(PluginIdentifiers.isValidId(pluginId));
  }

  @ParameterizedTest
  @ValueSource(strings = {"0.0.0", "1.0.0", "1.4.2", "2.0.0-rc.1", "1.0.0+build.7", "10.20.30"})
  void acceptsValidVersions(String version) {
    assertTrue(PluginIdentifiers.isValidVersion(version));
  }

  @ParameterizedTest
  @ValueSource(
      strings = {
        "",
        "1",
        "1.0",
        "01.0.0",
        "1.0.0/../x",
        "1.0.0-../x",
        "1.0.0+../x",
        "../1.0.0",
        "1.0.0\\x"
      })
  void rejectsInvalidOrUnsafeVersions(String version) {
    assertFalse(PluginIdentifiers.isValidVersion(version));
  }

  @Test
  void pathSafetyRejectsTraversalEvenWhenOtherChecksPass() {
    assertFalse(PluginIdentifiers.isPathSafeSegment("a/b"));
    assertFalse(PluginIdentifiers.isPathSafeSegment("a\\b"));
    assertFalse(PluginIdentifiers.isPathSafeSegment("a..b"));
    assertTrue(PluginIdentifiers.isPathSafeSegment("1.0.0-rc.1"));
  }
}
