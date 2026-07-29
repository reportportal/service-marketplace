package com.epam.reportportal.marketplace.domain;

import java.util.ArrayList;
import java.util.List;

public class IndexJson {

  private List<IndexPluginEntry> plugins = new ArrayList<>();

  public List<IndexPluginEntry> getPlugins() {
    return plugins;
  }

  public void setPlugins(List<IndexPluginEntry> plugins) {
    this.plugins = plugins;
  }
}
