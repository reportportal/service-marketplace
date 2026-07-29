package com.epam.reportportal.marketplace.service;

import com.epam.reportportal.marketplace.domain.PluginJson;
import com.epam.reportportal.marketplace.storage.ObjectStore;
import com.epam.reportportal.marketplace.util.JsonStore;
import com.epam.reportportal.marketplace.util.StoragePaths;
import tools.jackson.databind.ObjectMapper;
import java.util.HashSet;
import java.util.List;
import java.util.Set;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.scheduling.annotation.Scheduled;
import org.springframework.stereotype.Component;

@Component
public class OrphanCleanupJob {

  private static final Logger log = LoggerFactory.getLogger(OrphanCleanupJob.class);

  private final ObjectStore objectStore;
  private final ObjectMapper objectMapper;

  public OrphanCleanupJob(ObjectStore objectStore, ObjectMapper objectMapper) {
    this.objectStore = objectStore;
    this.objectMapper = objectMapper;
  }

  @Scheduled(cron = "${marketplace.orphan-cleanup.cron:0 0 3 * * *}")
  public void cleanupOrphans() {
    List<String> pluginKeys = objectStore.listPrefix(StoragePaths.pluginsPrefix());
    Set<String> pluginIds = new HashSet<>();
    for (String key : pluginKeys) {
      if (key.endsWith("/plugin.json")) {
        pluginIds.add(key.substring("plugins/".length(), key.length() - "/plugin.json".length()));
      }
    }
    for (String pluginId : pluginIds) {
      PluginJson plugin = JsonStore.read(objectStore, objectMapper, StoragePaths.pluginJson(pluginId), PluginJson.class);
      if (plugin == null) {
        continue;
      }
      Set<String> knownVersions = new HashSet<>(plugin.getVersions());
      String versionsPrefix = "plugins/" + pluginId + "/versions/";
      Set<String> discoveredVersions = new HashSet<>();
      for (String key : objectStore.listPrefix(versionsPrefix)) {
        int idx = key.indexOf("/versions/");
        if (idx < 0) {
          continue;
        }
        String remainder = key.substring(idx + "/versions/".length());
        int slash = remainder.indexOf('/');
        if (slash > 0) {
          discoveredVersions.add(remainder.substring(0, slash));
        }
      }
      for (String orphan : discoveredVersions) {
        if (!knownVersions.contains(orphan)) {
          log.info("Removing orphan version directory {}@{}", pluginId, orphan);
          String prefix = StoragePaths.versionDir(pluginId, orphan) + "/";
          for (String key : objectStore.listPrefix(prefix)) {
            objectStore.delete(key);
          }
        }
      }
    }
  }
}
