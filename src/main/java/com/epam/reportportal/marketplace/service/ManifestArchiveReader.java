package com.epam.reportportal.marketplace.service;

import com.epam.reportportal.marketplace.web.dto.ValidationFieldError;
import com.epam.reportportal.marketplace.web.error.ValidationException;
import java.io.ByteArrayInputStream;
import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.util.List;
import java.util.zip.ZipEntry;
import java.util.zip.ZipInputStream;

/**
 * Reads a single entry out of an untrusted publish archive under explicit resource limits.
 *
 * <p>Multipart limits bound how many bytes a publisher may upload; they say nothing about the
 * decompression work a crafted archive can force. Every scan here is therefore metered.
 */
final class ManifestArchiveReader {

  /** Entry-count cap: bounds the scan of a high-entry-count archive. */
  static final int MAX_SCANNED_ENTRIES = 10_000;

  /** Size cap for the requested entry: the manifest is a small JSON document, never megabytes. */
  static final int MAX_ENTRY_BYTES = 1024 * 1024;

  /** Inflated-byte budget across the whole scan, including entries skipped on the way. */
  static final long MAX_INFLATED_BYTES = 256L * 1024 * 1024;

  private static final int BUFFER_SIZE = 8192;

  private final int maxScannedEntries;
  private final int maxEntryBytes;
  private final long maxInflatedBytes;

  ManifestArchiveReader() {
    this(MAX_SCANNED_ENTRIES, MAX_ENTRY_BYTES, MAX_INFLATED_BYTES);
  }

  ManifestArchiveReader(int maxScannedEntries, int maxEntryBytes, long maxInflatedBytes) {
    this.maxScannedEntries = maxScannedEntries;
    this.maxEntryBytes = maxEntryBytes;
    this.maxInflatedBytes = maxInflatedBytes;
  }

  /**
   * Returns the bytes of {@code entryName}, or throws {@link ValidationException} when the entry is
   * absent, the archive is unreadable, or any limit is exceeded.
   */
  byte[] read(byte[] archiveBytes, String entryName) {
    long remainingBudget = maxInflatedBytes;
    int scannedEntries = 0;
    try (ZipInputStream zis = new ZipInputStream(new ByteArrayInputStream(archiveBytes))) {
      ZipEntry entry;
      while ((entry = zis.getNextEntry()) != null) {
        if (++scannedEntries > maxScannedEntries) {
          throw reject("Archive contains more than " + maxScannedEntries + " entries");
        }
        if (entryName.equals(entry.getName())) {
          return readEntry(zis, entryName, remainingBudget);
        }
        // Drain explicitly so skipped entries count against the budget; ZipInputStream would
        // otherwise inflate them unmetered while seeking the next header.
        remainingBudget = drain(zis, remainingBudget);
      }
    } catch (IOException e) {
      throw new ValidationException("Failed to read jar", List.of(
          new ValidationFieldError("jar", "Unable to read jar archive")));
    }
    throw new ValidationException("Missing manifest", List.of(
        new ValidationFieldError("manifest", entryName + " not found in jar")));
  }

  private byte[] readEntry(ZipInputStream zis, String entryName, long remainingBudget)
      throws IOException {
    long limit = Math.min(maxEntryBytes, remainingBudget);
    ByteArrayOutputStream out = new ByteArrayOutputStream();
    byte[] buffer = new byte[BUFFER_SIZE];
    int read;
    while ((read = zis.read(buffer)) != -1) {
      if (out.size() + read > limit) {
        throw reject(entryName + " exceeds the maximum size of " + maxEntryBytes + " bytes");
      }
      out.write(buffer, 0, read);
    }
    return out.toByteArray();
  }

  private long drain(ZipInputStream zis, long remainingBudget) throws IOException {
    byte[] buffer = new byte[BUFFER_SIZE];
    long budget = remainingBudget;
    int read;
    while ((read = zis.read(buffer)) != -1) {
      budget -= read;
      if (budget < 0) {
        throw reject(
            "Archive expands beyond the maximum of " + maxInflatedBytes + " decompressed bytes");
      }
    }
    return budget;
  }

  private ValidationException reject(String message) {
    return new ValidationException("Rejected jar archive", List.of(
        new ValidationFieldError("jar", message)));
  }
}
