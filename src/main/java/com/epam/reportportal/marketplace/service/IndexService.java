package com.epam.reportportal.marketplace.service;

import com.epam.reportportal.marketplace.domain.AccessTier;
import com.epam.reportportal.marketplace.domain.IndexJson;
import com.epam.reportportal.marketplace.domain.IndexPluginEntry;
import com.epam.reportportal.marketplace.domain.MarketplaceManifest;
import com.epam.reportportal.marketplace.domain.PluginJson;
import com.epam.reportportal.marketplace.storage.ObjectStore;
import com.epam.reportportal.marketplace.storage.OptimisticConcurrency;
import com.epam.reportportal.marketplace.util.JsonStore;
import com.epam.reportportal.marketplace.util.StoragePaths;
import com.fasterxml.jackson.databind.ObjectMapper;
import java.util.List;
import org.springframework.stereotype.Service;

@Service
public class IndexService {

  private final ObjectStore objectStore;
  private final ObjectMapper objectMapper;

  public IndexService(ObjectStore objectStore, ObjectMapper objectMapper) {
    this.objectStore = objectStore;
    this.objectMapper = objectMapper;
  }

  public void regenerateIndex() {
    OptimisticConcurrency.execute(() -> {
      String indexPath = StoragePaths.INDEX;
      long generation = objectStore.getGeneration(indexPath).orElse(-1L);
      IndexJson index = new IndexJson();
      List<String> pluginKeys = objectStore.listPrefix(StoragePaths.pluginsPrefix());
      List<String> pluginIds = pluginKeys.stream()
          .filter(k -> k.endsWith("/plugin.json"))
          .map(k -> k.substring("plugins/".length(), k.length() - "/plugin.json".length()))
          .distinct()
          .sorted()
          .toList();

      for (String pluginId : pluginIds) {
        PluginJson plugin = JsonStore.read(objectStore, objectMapper, StoragePaths.pluginJson(pluginId), PluginJson.class);
        if (plugin == null || plugin.isRemoved()) {
          continue;
        }
        String latest = plugin.getLatestVersion();
        if (latest == null) {
          continue;
        }
        MarketplaceManifest manifest = JsonStore.read(
            objectStore, objectMapper, StoragePaths.manifestPath(pluginId, latest), MarketplaceManifest.class);
        if (manifest == null) {
          continue;
        }
        index.getPlugins().add(new IndexPluginEntry(
            pluginId,
            manifest.name(),
            latest,
            manifest.description(),
            manifest.category(),
            manifest.access() != null ? manifest.access() : AccessTier.PUBLIC,
            plugin.getTier()));
      }
      JsonStore.writeIfGenerationMatch(objectStore, objectMapper, indexPath, index, generation);
      return null;
    });
  }

  public IndexJson getIndex() {
    IndexJson index = JsonStore.read(objectStore, objectMapper, StoragePaths.INDEX, IndexJson.class);
    return index != null ? index : new IndexJson();
  }
}
