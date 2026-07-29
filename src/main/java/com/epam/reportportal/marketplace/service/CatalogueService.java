package com.epam.reportportal.marketplace.service;

import com.epam.reportportal.marketplace.config.MarketplaceProperties;
import com.epam.reportportal.marketplace.domain.AdvisoryJson;
import com.epam.reportportal.marketplace.domain.AssetsJson;
import com.epam.reportportal.marketplace.domain.BlockedVersion;
import com.epam.reportportal.marketplace.domain.IndexJson;
import com.epam.reportportal.marketplace.domain.IndexPluginEntry;
import com.epam.reportportal.marketplace.domain.MarketplaceManifest;
import com.epam.reportportal.marketplace.domain.PluginJson;
import com.epam.reportportal.marketplace.storage.ObjectStore;
import com.epam.reportportal.marketplace.util.JsonStore;
import com.epam.reportportal.marketplace.util.StoragePaths;
import com.epam.reportportal.marketplace.web.dto.PluginDetailDto;
import com.epam.reportportal.marketplace.web.dto.PluginListItemDto;
import com.epam.reportportal.marketplace.web.dto.PluginListResponseDto;
import com.epam.reportportal.marketplace.web.dto.PluginTombstoneDto;
import com.epam.reportportal.marketplace.web.dto.PluginVersionDetailDto;
import com.epam.reportportal.marketplace.web.dto.PluginVersionListResponseDto;
import com.epam.reportportal.marketplace.web.dto.PluginVersionSummaryDto;
import com.epam.reportportal.marketplace.web.error.GoneException;
import com.epam.reportportal.marketplace.web.error.NotFoundException;
import com.fasterxml.jackson.databind.ObjectMapper;
import java.util.ArrayList;
import java.util.List;
import java.util.Locale;
import java.util.Optional;
import org.springframework.stereotype.Service;

@Service
public class CatalogueService {

  private final ObjectStore objectStore;
  private final ObjectMapper objectMapper;
  private final IndexService indexService;
  private final MarketplaceProperties properties;

  public CatalogueService(
      ObjectStore objectStore,
      ObjectMapper objectMapper,
      IndexService indexService,
      MarketplaceProperties properties) {
    this.objectStore = objectStore;
    this.objectMapper = objectMapper;
    this.indexService = indexService;
    this.properties = properties;
  }

  public PluginListResponseDto listPlugins(String category, String query) {
    IndexJson index = indexService.getIndex();
    List<PluginListItemDto> items = new ArrayList<>();
    for (IndexPluginEntry entry : index.getPlugins()) {
      if (category != null && !category.isBlank()
          && !entry.category().value().equalsIgnoreCase(category)) {
        continue;
      }
      if (query != null && !query.isBlank()) {
        String q = query.toLowerCase(Locale.ROOT);
        String haystack = (entry.name() + " " + Optional.ofNullable(entry.description()).orElse(""))
            .toLowerCase(Locale.ROOT);
        if (!haystack.contains(q)) {
          continue;
        }
      }
      items.add(new PluginListItemDto(
          entry.id(),
          entry.name(),
          entry.latestVersion(),
          entry.description(),
          entry.category(),
          entry.access(),
          entry.tier()));
    }
    return new PluginListResponseDto(items);
  }

  public PluginDetailDto getPlugin(String pluginId) {
    PluginJson plugin = loadPlugin(pluginId);
    if (plugin.isRemoved()) {
      throw new GoneException(toTombstone(plugin));
    }
    MarketplaceManifest manifest = loadManifest(pluginId, plugin.getLatestVersion());
    return PluginDetailDto.from(manifest, plugin.getTier(), plugin.getLatestVersion());
  }

  public PluginVersionListResponseDto listVersions(String pluginId) {
    PluginJson plugin = loadPlugin(pluginId);
    if (plugin.isRemoved()) {
      throw new GoneException(toTombstone(plugin));
    }
    List<PluginVersionSummaryDto> versions = new ArrayList<>();
    for (String version : plugin.getVersions()) {
      BlockedVersion blocked = findBlocked(plugin, version);
      AssetsJson assets = JsonStore.read(
          objectStore, objectMapper, StoragePaths.assetsPath(pluginId, version), AssetsJson.class);
      versions.add(new PluginVersionSummaryDto(
          version,
          assets != null ? assets.getPublishedAt() : null,
          blocked != null,
          blocked != null ? blocked.blockedAt() : null,
          blocked != null ? blocked.reason() : null));
    }
    return new PluginVersionListResponseDto(pluginId, versions);
  }

  public PluginVersionDetailDto getVersionDetail(String pluginId, String version) {
    PluginJson plugin = loadPlugin(pluginId);
    if (plugin.isRemoved()) {
      throw new GoneException(toTombstone(plugin));
    }
    if (!plugin.getVersions().contains(version)) {
      throw new NotFoundException("Version not found: " + version);
    }
    MarketplaceManifest manifest = loadManifest(pluginId, version);
    AssetsJson assets = JsonStore.read(
        objectStore, objectMapper, StoragePaths.assetsPath(pluginId, version), AssetsJson.class);
    AdvisoryJson advisory = JsonStore.read(
        objectStore, objectMapper, StoragePaths.advisoryPath(pluginId, version), AdvisoryJson.class);
    BlockedVersion blocked = findBlocked(plugin, version);
    String base = properties.getCdn().getBaseUrl().replaceAll("/$", "");
    String versionBase = base + "/plugins/" + pluginId + "/versions/" + version;
    String changelogUrl = assets != null && assets.isHasChangelog()
        ? versionBase + "/CHANGELOG.md"
        : null;
    List<String> screenshotUrls = new ArrayList<>();
    if (assets != null && assets.getScreenshots() != null) {
      for (String name : assets.getScreenshots()) {
        screenshotUrls.add(versionBase + "/screenshots/" + name);
      }
    }
    return PluginVersionDetailDto.from(
        manifest,
        plugin.getTier(),
        blocked != null,
        blocked != null ? blocked.blockedAt() : null,
        blocked != null ? blocked.reason() : null,
        advisory,
        assets != null ? assets.getSha256() : null,
        changelogUrl,
        screenshotUrls);
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

  private MarketplaceManifest loadManifest(String pluginId, String version) {
    MarketplaceManifest manifest = JsonStore.read(
        objectStore, objectMapper, StoragePaths.manifestPath(pluginId, version), MarketplaceManifest.class);
    if (manifest == null) {
      throw new NotFoundException("Manifest not found for " + pluginId + "@" + version);
    }
    return manifest;
  }

  private BlockedVersion findBlocked(PluginJson plugin, String version) {
    if (plugin.getBlockedVersions() == null) {
      return null;
    }
    return plugin.getBlockedVersions().stream()
        .filter(b -> version.equals(b.version()))
        .findFirst()
        .orElse(null);
  }

  private PluginTombstoneDto toTombstone(PluginJson plugin) {
    return new PluginTombstoneDto(plugin.getRemoved(), plugin.getRemovalReason(), plugin.getRemovedBy());
  }
}
