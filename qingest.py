#!/usr/bin/env python3
"""
qingest.py

Walk a directory (optionally recursively) of Markdown files, embed their
contents via an OpenAI-compatible embedding endpoint, and persist the
results into a Qdrant collection.

Usage examples
--------------
  # Embed all .md files in ./docs into collection "knowledge"
  python qingest.py --dir ./docs --collection knowledge

  # Create the collection if it doesn't exist
  python qingest.py --dir ./docs --collection knowledge \
      --embed-url http://127.0.0.1:8008/v1 \
      --qdrant-url http://localhost:6333 \
      --create-collection

  # Skip subdirectories (only top-level .md files)
  python qingest.py --dir ./docs --no-recursive

  # Custom chunking parameters
  python qingest.py --dir ./docs --chunk-size 1000 --chunk-overlap 200

  # Use a specific embedding model
  python qingest.py --dir ./docs --embed-model bge-large-en

  # Force re-embedding of already-indexed files
  python qingest.py --dir ./docs --force
"""

from __future__ import annotations

import argparse
import hashlib
import json
import logging
import os
import sys
import time
import uuid
from dataclasses import dataclass
from pathlib import Path
from typing import Optional

from dotenv import load_dotenv

import requests
from qdrant_client import QdrantClient
from qdrant_client.models import Distance, VectorParams, PointStruct, Filter, FieldCondition, MatchAny, PayloadSchemaType

# LangChain imports for advanced chunking
from langchain_text_splitters import MarkdownTextSplitter

# Load .env into os.environ (CLI args still override these)
load_dotenv()

def get_file_hash(filepath: str) -> str:
    """Calculate the SHA256 hash of a file."""
    hasher = hashlib.sha256()
    with open(filepath, 'rb') as f:
        for chunk in iter(lambda: f.read(4096), b""):
            hasher.update(chunk)
    return hasher.hexdigest()

# ---------------------------------------------------------------------------
# Logging
# ---------------------------------------------------------------------------
logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(levelname)s] %(message)s",
    datefmt="%Y-%m-%d %H:%M:%S",
)
log = logging.getLogger("embed_markdown")

# ---------------------------------------------------------------------------
# Data classes
# ---------------------------------------------------------------------------

@dataclass
class Chunk:
    """A text chunk ready for embedding."""
    file_path: str          # original file path (relative to --dir)
    chunk_index: int        # 0-based index within the file
    content: str            # raw text
    file_hash: str = ""     # SHA256 of the source file, stored as top-level DB field
    metadata: dict = None   # extra metadata

    def __post_init__(self):
        if self.metadata is None:
            self.metadata = {}


# ---------------------------------------------------------------------------
# Chunking logic
# ---------------------------------------------------------------------------

def chunk_markdown_file(
    file_path: str,
    abs_path: str,
    file_hash: str,
    chunk_size: int = 800,
    chunk_overlap: int = 200,
) -> list[Chunk]:
    """Split a markdown file's text into semantic chunks using LangChain."""
    try:
        with open(abs_path, "r", encoding="utf-8", errors="replace") as f:
            text = f.read()
    except Exception as exc:
        log.error("Failed to read file '%s': %s", abs_path, exc)
        return []

    splitter = MarkdownTextSplitter(
        chunk_size=chunk_size,
        chunk_overlap=chunk_overlap,
    )
    
    # Split the markdown text
    st_chunks = splitter.split_text(text)
    
    return [
        Chunk(
            file_path=file_path,
            chunk_index=i,
            content=chunk_text,
            file_hash=file_hash,
            metadata={
                "source_file": file_path,
                "total_chunks": len(st_chunks),
            },
        )
        for i, chunk_text in enumerate(st_chunks)
        if chunk_text.strip()
    ]


# ---------------------------------------------------------------------------
# Embedding (OpenAI-compatible endpoint)
# ---------------------------------------------------------------------------

_EMBED_MODEL_DEFAULT = "bge-m3"

def _embed_batch(
    texts: list[str],
    embed_url: str,
    model: str = _EMBED_MODEL_DEFAULT,
    batch_size: int = 128,
) -> list[list[float]]:
    """
    Call an OpenAI-compatible /v1/embeddings endpoint in batches.

    Returns a list of embedding vectors, one per input text.
    """
    all_embeddings: list[list[float]] = []

    for i in range(0, len(texts), batch_size):
        batch = texts[i : i + batch_size]
        log.info(
            "Embedding batch %d–%d / %d",
            i + 1,
            min(i + batch_size, len(texts)),
            len(texts),
        )

        payload = {
            "model": model,
            "input": batch,
        }

        resp = requests.post(
            f"{embed_url}/embeddings",
            json=payload,
            timeout=300,
        )
        resp.raise_for_status()

        body = resp.json()
        # OpenAI-compatible shape: { data: [ { embedding: [...] }, ... ] }
        embeddings = [item["embedding"] for item in body["data"]]
        all_embeddings.extend(embeddings)

    return all_embeddings


# ---------------------------------------------------------------------------
# Qdrant DB interaction
# ---------------------------------------------------------------------------

def qdrant_create_collection_if_not_exists(
    client: QdrantClient,
    collection: str,
    vector_dim: int,
) -> None:
    """Create collection if it doesn't already exist and add payload index."""
    try:
        if not client.collection_exists(collection_name=collection):
            log.info("Creating collection '%s' (vector dim=%d).", collection, vector_dim)
            client.create_collection(
                collection_name=collection,
                vectors_config=VectorParams(size=vector_dim, distance=Distance.COSINE),
            )
            # Create index on file_path for fast filtering/scrolling
            client.create_payload_index(
                collection_name=collection,
                field_name="file_path",
                field_schema=PayloadSchemaType.KEYWORD,
            )
            log.info("Collection '%s' created and payload index ensured.", collection)
        else:
            log.info("Collection '%s' already exists.", collection)
    except Exception as exc:
        log.error("Failed to ensure/create Qdrant collection '%s': %s", collection, exc)
        raise


def qdrant_get_indexed_paths(
    client: QdrantClient,
    collection: str,
    filter_paths: Optional[list[str]] = None,
) -> dict[str, str]:
    """Retrieve indexed file paths and their hashes from Qdrant."""
    if not filter_paths:
        return {}

    path_hashes: dict[str, str] = {}
    try:
        # Scroll points matching the filter_paths in batches
        points, _ = client.scroll(
            collection_name=collection,
            scroll_filter=Filter(
                must=[
                    FieldCondition(
                        key="file_path",
                        match=MatchAny(any=filter_paths),
                    )
                ]
            ),
            limit=10000,
            with_payload=["file_path", "file_hash"],
            with_vectors=False,
        )
        for point in points:
            payload = point.payload or {}
            fp = payload.get("file_path")
            fh = payload.get("file_hash")
            if fp:
                path_hashes[fp] = fh if fh else "__legacy__"
    except Exception as exc:
        log.warning("Failed to query indexed paths from Qdrant collection '%s': %s", collection, exc)

    log.debug("Found %d indexed path(s) in Qdrant collection '%s'.", len(path_hashes), collection)
    return path_hashes


def qdrant_delete_by_paths(
    client: QdrantClient,
    collection: str,
    file_paths: set[str],
) -> int:
    """Delete all records matching any of the given file_path values."""
    if not file_paths:
        return 0
    try:
        client.delete(
            collection_name=collection,
            points_selector=Filter(
                must=[
                    FieldCondition(
                        key="file_path",
                        match=MatchAny(any=list(file_paths)),
                    )
                ]
            ),
        )
        log.debug("Deleted records for file_paths=%s.", file_paths)
        return len(file_paths)
    except Exception as exc:
        log.error("Failed to delete records for file_paths in Qdrant: %s", exc)
        return 0


def qdrant_store_embeddings(
    client: QdrantClient,
    collection: str,
    chunks: list[Chunk],
    embeddings: list[list[float]],
    dry_run: bool = False,
) -> int:
    """Store chunks + embeddings into Qdrant."""
    from datetime import datetime, timezone
    count = 0
    now = datetime.now(timezone.utc)
    points = []
    
    for chunk, emb in zip(chunks, embeddings):
        if dry_run:
            log.info("[DRY-RUN] Would insert chunk from %s (idx %d, %d dims)",
                     chunk.file_path, chunk.chunk_index, len(emb))
            count += 1
            continue
            
        point_id = str(uuid.uuid4())
        payload = {
            "content": chunk.content,
            "file_path": chunk.file_path,
            "file_hash": chunk.file_hash,
            "chunk_index": chunk.chunk_index,
            "metadata": chunk.metadata,
            "indexed_at": now.isoformat(),
        }
        points.append(PointStruct(id=point_id, vector=emb, payload=payload))
        
    if dry_run or not points:
        return count

    try:
        client.upsert(collection_name=collection, points=points)
        count = len(points)
    except Exception as exc:
        log.error("Failed to upsert chunks to Qdrant collection '%s': %s", collection, exc)
        
    return count


# ---------------------------------------------------------------------------
# File discovery
# ---------------------------------------------------------------------------

def discover_md_files(
    directory: str,
    recursive: bool = True,
    extension: str = ".md",
) -> list[str]:
    """Return sorted list of .md file paths under *directory*."""
    root = Path(directory).resolve()
    pattern = "**" if recursive else ""
    files = sorted(root.glob(f"{pattern}/*{extension}"))
    return [str(f) for f in files]


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

def main() -> None:
    parser = argparse.ArgumentParser(
        description="Embed markdown files into Qdrant via an embedding API.",
    )
    parser.add_argument(
        "--dir", "-d",
        required=True,
        help="Root directory containing .md files.",
    )
    parser.add_argument(
        "--embed-url",
        default=os.environ.get("QDRANT_EMBED_URL", os.environ.get("SURREAL_EMBED_URL", "http://127.0.0.1:8008/v1")),
        help="Base URL of the OpenAI-compatible embedding API.",
    )
    parser.add_argument(
        "--embed-model",
        default=os.environ.get("QDRANT_EMBED_MODEL", os.environ.get("SURREAL_EMBED_MODEL", _EMBED_MODEL_DEFAULT)),
        help="Embedding model name (default: %s)." % _EMBED_MODEL_DEFAULT,
    )
    parser.add_argument(
        "--qdrant-url",
        default=os.environ.get("QDRANT_URL", "http://localhost:6333"),
        help="Qdrant API URL.",
    )
    parser.add_argument(
        "--qdrant-api-key",
        default=os.environ.get("QDRANT_API_KEY", None),
        help="Qdrant API Key (optional).",
    )
    parser.add_argument(
        "--collection",
        default=os.environ.get("QDRANT_COLLECTION", os.environ.get("SURREAL_TABLE", "mdchunk")),
        help="Qdrant collection to store chunks into.",
    )
    parser.add_argument(
        "--no-recursive",
        action="store_true",
        help="Only scan the top-level directory (no subdirectories).",
    )
    parser.add_argument(
        "--chunk-size",
        type=int,
        default=800,
        help="Target chunk size in characters.",
    )
    parser.add_argument(
        "--chunk-overlap",
        type=int,
        default=200,
        help="Overlap between chunks in characters.",
    )
    parser.add_argument(
        "--create-collection",
        action="store_true",
        help="Create the target collection if it doesn't exist.",
    )
    parser.add_argument(
        "--dry-run",
        action="store_true",
        help="Walk files, chunk, embed, but do NOT write to Qdrant.",
    )
    parser.add_argument(
        "--batch-size",
        type=int,
        default=128,
        help="Number of texts to embed per API call.",
    )
    parser.add_argument(
        "--doc-batch-size",
        type=int,
        default=5,
        help="Number of documents to process and ingest as a single batch (default: 5).",
    )
    parser.add_argument(
        "--force", "-f",
        action="store_true",
        help="Re-embed and re-insert files that are already in the DB (delete old records first).",
    )
    parser.add_argument(
        "--verbose", "-v",
        action="store_true",
        help="Verbose (debug) logging.",
    )

    args = parser.parse_args()

    if args.verbose:
        logging.getLogger().setLevel(logging.DEBUG)

    directory = args.dir
    if not os.path.isdir(directory):
        log.error("Directory '%s' does not exist.", directory)
        sys.exit(1)

    # --- 1. Discover files ---
    files = discover_md_files(directory, recursive=not args.no_recursive)
    log.info("Found %d markdown file(s) in '%s'.", len(files), directory)
    log.info("Using Qdrant: %s (Collection: %s)", args.qdrant_url, args.collection)
    
    if not files:
        log.warning("No .md files found. Exiting.")
        sys.exit(0)

    # Initialize Qdrant client
    client = QdrantClient(url=args.qdrant_url, api_key=args.qdrant_api_key)

    collection_ensured = False

    # --- 2. Process in batches of documents ---
    doc_batch_size = args.doc_batch_size
    total_files = len(files)
    total_inserted = 0
    total_skipped = 0

    log.info("Processing %d files in batches of %d documents.", total_files, doc_batch_size)

    for i in range(0, total_files, doc_batch_size):
        batch_files = files[i : i + doc_batch_size]
        batch_num = (i // doc_batch_size) + 1
        total_batches = (total_files + doc_batch_size - 1) // doc_batch_size
        
        log.info("--- Batch %d/%d (Files %d-%d of %d) ---", 
                 batch_num, total_batches, i+1, min(i+doc_batch_size, total_files), total_files)
        
        # a. Idempotency check: compute relative paths and current hashes for this batch
        batch_rel_paths = [os.path.relpath(f, directory) for f in batch_files]
        batch_hashes = {os.path.relpath(f, directory): get_file_hash(f) for f in batch_files}
        
        indexed_file_hashes: dict[str, str] = {}
        if not args.dry_run:
            try:
                indexed_file_hashes = qdrant_get_indexed_paths(
                    client, args.collection, filter_paths=batch_rel_paths
                )
            except Exception as exc:
                log.warning("Could not query existing hashes for batch (will process all): %s", exc)

        files_to_process_this_batch: list[tuple[str, str, str]] = [] # (abs, rel, hash)
        paths_to_evict_this_batch: set[str] = set()

        for f in batch_files:
            rel = os.path.relpath(f, directory)
            current_hash = batch_hashes[rel]
            exists_in_db = rel in indexed_file_hashes
            stored_hash = indexed_file_hashes.get(rel)

            should_process = False
            reason = ""

            if not exists_in_db:
                should_process = True
                reason = "New file"
            elif args.force:
                should_process = True
                reason = "Force replace (--force)"
                paths_to_evict_this_batch.add(rel)
            elif stored_hash == "__legacy__":
                # Already in DB but predates hash tracking — skip unless --force
                log.info("⏭️  %s: Already indexed (legacy record). Skipping.", rel)
                should_process = False
            elif stored_hash != current_hash:
                should_process = True
                reason = "Modified (hash mismatch)"
                paths_to_evict_this_batch.add(rel)
            else:
                log.info("⏭️  %s: Identical (hash match). Skipping.", rel)
                should_process = False

            if should_process:
                log.info("🚀 Processing: %s (%s)", rel, reason)
                files_to_process_this_batch.append((f, rel, current_hash))
            else:
                total_skipped += 1

        if not files_to_process_this_batch:
            log.info("No files to update in this batch.")
            continue

        if not args.dry_run and paths_to_evict_this_batch:
            qdrant_delete_by_paths(client, args.collection, paths_to_evict_this_batch)

        # b. Read + chunk
        batch_chunks: list[Chunk] = []
        for abs_path, rel_path, f_hash in files_to_process_this_batch:
            chunks = chunk_markdown_file(rel_path, abs_path, f_hash, args.chunk_size, args.chunk_overlap)
            batch_chunks.extend(chunks)
            log.info("File '%s' → %d chunk(s)", rel_path, len(chunks))

        if not batch_chunks:
            continue

        # c. Embed
        log.info("Embedding %d chunks...", len(batch_chunks))
        texts = [c.content for c in batch_chunks]
        try:
            embeddings = _embed_batch(texts, args.embed_url, args.embed_model, args.batch_size)
        except Exception as exc:
            log.error("Embedding failed for batch %d: %s", batch_num, exc)
            continue

        # d. Ensure collection schema (once, after we know vector dim)
        if args.create_collection and not collection_ensured:
            vector_dim = len(embeddings[0])
            try:
                qdrant_create_collection_if_not_exists(client, args.collection, vector_dim)
                collection_ensured = True
            except Exception as exc:
                log.error("Failed to create Qdrant collection: %s", exc)
                sys.exit(1)

        # e. Store
        if not args.dry_run:
            inserted = qdrant_store_embeddings(
                client, args.collection, batch_chunks, embeddings,
                dry_run=False,
            )
            total_inserted += inserted
            log.info("Batch %d: Inserted %d record(s).", batch_num, inserted)
        else:
            qdrant_store_embeddings(
                client, args.collection, batch_chunks, embeddings,
                dry_run=True,
            )
            log.info("Batch %d: Dry-run complete.", batch_num)

    if not args.dry_run:
        log.info("Total inserted: %d record(s), skipped: %d file(s).",
                 total_inserted, total_skipped)
    else:
        log.info("Dry-run complete. No data written to Qdrant.")

    log.info("Done.")


if __name__ == "__main__":
    main()