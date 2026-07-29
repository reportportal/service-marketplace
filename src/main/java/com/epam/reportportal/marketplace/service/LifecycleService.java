package com.epam.reportportal.marketplace.service;

import com.epam.reportportal.marketplace.domain.BlockedVersion;
import com.epam.reportportal.marketplace.domain.PluginJson;
import com.epam.reportportal.marketplace.domain.TrustTier;
import com.epam.reportportal.marketplace.storage.ObjectStore;
import com.epam.reportportal.marketplace.storage.OptimisticConcurrency;
import com.epam.reportportal.marketplace.util.JsonStore;
import com.epam.reportportal.marketplace.util.StoragePaths;
import com.epam.reportportal.marketplace.web.dto.PluginOperatorStateDto;
import com.epam.reportportal.marketplace.web.dto.PluginTombstoneDto;
import com.epam.reportportal.marketplace.web.error.NotFoundException;
import com.epam.reportportal.marketplace.web.error.ValidationException;
import com.epam.reportportal.marketplace.web.dto.ValidationFieldError;
import tools.jackson.databind.ObjectMapper;
import java.time.Instant;
import java.util.List;
import org.springframework.stereotype.Service;

@Service
public class LifecycleService {

  private final ObjectStore objectStore;
  private final ObjectMapper objectMapper;
  private final IndexService indexService;
  private final CdnInvalidationService cdnInvalidationService;

  public LifecycleService(
      ObjectStore objectStore,
      ObjectMapper objectMapper,
      IndexService indexService,
      CdnInvalidationService cdnInvalidationService) {
    this.objectStore = objectStore;
    this.objectMapper = objectMapper;
    this.indexService = indexService;
    this.cdnInvalidationService = cdnInvalidationService;
  }

  public BlockedVersion blockVersion(String pluginId, String version, String reason) {
    return OptimisticConcurrency.execute(() -> {
      PluginJson plugin = loadPlugin(pluginId);
      if (!plugin.getVersions().contains(version)) {
        throw new NotFoundException("Version not found: " + version);
      }
      boolean alreadyBlocked = plugin.getBlockedVersions().stream().anyMatch(b -> version.equals(b.version()));
      if (!alreadyBlocked) {
        plugin.getBlockedVersions().add(new BlockedVersion(version, Instant.now(), reason));
      }
      writePlugin(plugin);
      OptimisticConcurrency.execute(() -> {
        indexService.regenerateIndex();
        return null;
      });
      cdnInvalidationService.invalidatePaths(List.of(
          "/" + StoragePaths.INDEX,
          "/plugins/" + pluginId + "/plugin.json",
          "/plugins/" + pluginId + "/versions/" + version + "/*"));
      return plugin.getBlockedVersions().stream()
          .filter(b -> version.equals(b.version()))
          .findFirst()
          .orElseThrow();
    });
  }

  public PluginTombstoneDto removePlugin(String pluginId, String removalReason, String removedBy) {
    PluginJson plugin = loadPlugin(pluginId);
    for (String version : List.copyOf(plugin.getVersions())) {
      deleteVersionArtifacts(pluginId, version);
    }
    plugin.setRemoved(Instant.now());
    plugin.setRemovalReason(removalReason);
    plugin.setRemovedBy(removedBy);
    plugin.getVersions().clear();
    plugin.setLatestVersion(null);
    writePlugin(plugin);
    OptimisticConcurrency.execute(() -> {
      indexService.regenerateIndex();
      return null;
    });
    cdnInvalidationService.invalidatePaths(List.of(
        "/" + StoragePaths.INDEX,
        "/plugins/" + pluginId + "/*"));
    return new PluginTombstoneDto(plugin.getRemoved(), plugin.getRemovalReason(), plugin.getRemovedBy());
  }

  public PluginOperatorStateDto patchTier(String pluginId, TrustTier tier) {
    if (tier != TrustTier.OFFICIAL) {
      throw new ValidationException("Only official tier is accepted", List.of(
          new ValidationFieldError("tier", "Only tier=official is accepted in Phase 1/2")));
    }
    return OptimisticConcurrency.execute(() -> {
      PluginJson plugin = loadPlugin(pluginId);
      if (plugin.isRemoved()) {
        throw new NotFoundException("Plugin not found: " + pluginId);
      }
      plugin.setTier(tier);
      writePlugin(plugin);
      OptimisticConcurrency.execute(() -> {
        indexService.regenerateIndex();
        return null;
      });
      return new PluginOperatorStateDto(
          plugin.getId(), plugin.getTier(), plugin.getLatestVersion(), plugin.getBlockedVersions());
    });
  }

  private void deleteVersionArtifacts(String pluginId, String version) {
    String prefix = StoragePaths.versionDir(pluginId, version) + "/";
    for (String key : objectStore.listPrefix(prefix)) {
      objectStore.delete(key);
    }
  }

  private PluginJson loadPlugin(String pluginId) {
    String path = StoragePaths.pluginJson(pluginId);
    if (!objectStore.exists(path)) {
      throw new NotFoundException("Plugin not found: " + pluginId);
    }
    PluginJson plugin = JsonStore.read(objectStore, objectMapper, path, PluginJson.class);
    if (plugin == null) {
      throw new NotFoundException("Plugin not found: " + pluginId);
    }
    return plugin;
  }

  private void writePlugin(PluginJson plugin) {
    String path = StoragePaths.pluginJson(plugin.getId());
    long generation = objectStore.getGeneration(path).orElse(-1L);
    JsonStore.writeIfGenerationMatch(objectStore, objectMapper, path, plugin, generation);
  }
}
